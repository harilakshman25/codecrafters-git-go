# `mygit` - Custom Git Client in Go

`mygit` is a lightweight, pure-Go implementation of a Git client built from scratch. It supports fundamental core object manipulation commands (`init`, `cat-file`, `hash-object`, `ls-tree`, `write-tree`, `commit-tree`) as well as advanced network features like Smart HTTP cloning, packfile parsing, and delta compression resolution.

---

## Architecture & Core Protocols

### 1. Object Database Storage

Git stores everything as content-addressable objects under `.git/objects/xx/yy...`, where `xx` is the first two characters of the object's SHA-1 hash and `yy...` is the remaining 38 characters.

* **Header Structure**: Every object is prepended with a type and size header followed by a null byte (`\x00`), e.g., `blob 14\x00content`.
* **Compression**: Objects are compressed using standard `zlib` before being written to disk.



### 2. Smart HTTP Protocol (`pkt-line`)

Communication with remote repositories over HTTP uses the **packet-line (pkt-line)** format.

* **Length Prefixing**: Every line/packet is prefixed with a 4-hex-digit length indicator (which includes the 4 bytes of the length prefix itself).


* **Discovery (`/info/refs`)**: The client reads the advertisement stream, skips the service line and flush packets (`0000`), and extracts the target reference SHA (e.g., `HEAD` or `refs/heads/master`).


* **Upload Pack (`/git-upload-pack`)**: Sends `want <SHA> <capabilities>` packets followed by a flush packet (`0000`) and a `done` packet to trigger packfile generation from the server.



### 3. Packfile Parsing & Delta Resolution

When cloning a repository, the remote server responds with a binary packfile containing compressed objects. `mygit` handles this in a robust two-pass strategy:

* **Pass 1 (Base Objects & Queueing)**: Parses the packfile header (`PACK`, version, object count), extracts non-delta types (commits, trees, blobs, tags), writes them to the object database, and queues `REF_DELTA` objects (`objType == 7`) into a pending queue.


* **Pass 2 (Multi-Pass Delta Resolution)**: Resolves delta chains iteratively. Because packfiles can list deltas before their base objects appear, a multi-pass approach handles out-of-order entries by re-queuing unresolved deltas until all base dependencies are satisfied on disk.


* **Delta Execution**: Implements binary patch instructions (`INSERT` and `COPY` with variable-length offsets and sizes) to reconstruct modified target payloads from base objects.



---

## Supported Commands Summary

| Command | Description |
| --- | --- |
| `init` | Initializes a new local `.git` directory structure (`objects`, `refs/heads`, and `HEAD`).|
| `cat-file -p <sha>` | Decodes, decompresses, strips headers, and prints the content of any Git object.|
| `hash-object -w <file>` | Hashes a file with the proper Git blob header, compresses it, and saves it to the object database.|
| `ls-tree --name-only <sha>` | Parses a tree object and lists the filenames it contains.|
| `write-tree` | Recursively writes the current working directory state into tree objects, returning the root SHA.|
| `commit-tree <tree> -p <parent> -m <msg>` | Creates a commit object linking a tree to a parent commit with author/committer metadata.|
| `clone <url> <dir>` | Performs Smart HTTP discovery, downloads packfiles, unpacks/resolves deltas, and checks out files into the target directory.|

---

## Technical & Architectural Decisions

* **Stream Parsing with `bufio.Reader**`: Standard `bytes.Reader` lacks delimiter-based scanning. Wrapping stream readers with `bufio.Reader` enables precise tracking using `.ReadBytes(' ')` and `.ReadBytes(0)` when parsing tree entries or packet lines.


* **Defensive Error Handling for EOF**: Parsing loops explicitly check for `io.EOF` to terminate safely without triggering unexpected stream truncation errors.


* **Side-Band Channel Multiplexing**: The packfile payload reader inspects channel side-band headers (`payload[0]`) to gracefully filter progress messages (`channel 2`) while capturing raw pack data (`channel 1`).



---

## Debugging & Troubleshooting Log

### 1. The `ReadBytes` vs. `ReadByte` Type Discrepancy

* **Issue**: Initially, code attempted to invoke `reader.ReadBytes(' ')` directly on a standard `bytes.Reader`, resulting in a compilation error (`reader.ReadBytes undefined`).


* **Resolution**: Wrapped the underlying reader inside a `bufio.Reader` (`bufio.NewReader(bytes.NewReader(content))`), which natively supports delimited reads required for separating object permissions, file names, and null bytes.



### 2. Working Directory & Path Duplication during Checkout

* **Issue**: During `clone`, changing the working directory via `os.Chdir(targetDir)` while concurrently passing `targetDir` down to tree checkout functions resulted in redundant nested paths (e.g., `test_dir/test_dir/file.txt`), triggering `no such file or directory` runtime crashes.


* **Resolution**: Standardized the checkout root to use relative paths (`"."`) immediately after switching the working process directory to `targetDir`, ensuring paths evaluate cleanly from the root of the newly cloned folder.