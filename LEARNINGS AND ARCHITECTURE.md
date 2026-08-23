# mygit: Implementation and Architecture

`mygit` is a small Go program that implements selected local Git operations and
the parts of Smart HTTP cloning needed to download, unpack, and check out a
repository. All implementation is currently in `app/main.go`. It uses only Go
standard-library packages and stores objects in the current directory's `.git`
directory.

## Program Structure

`main` is the command dispatcher. It requires a command name in `os.Args[1]`,
routes the command to a handler, prints handler errors to standard error, and
exits with status 1 on invalid commands or failures.

The implementation is organized into four functional areas:

1. Command handlers validate arguments and provide command-line output.
2. Object-database helpers read and write compressed Git objects.
3. Pack and protocol helpers implement the binary formats used by clone.
4. Checkout helpers turn downloaded commit, tree, and blob objects into files.

There is no separate repository, object, transport, or checkout type. These
areas are implemented as package-level functions and use the process working
directory as the repository context.

## Supported Commands

### `init`

Creates `.git`, `.git/objects`, `.git/refs`, and `.git/refs/heads` with mode
`0755`. It writes `.git/HEAD` containing:

```text
ref: refs/heads/master
```

It does not create a branch reference or any other Git configuration files.

### `cat-file -p <sha>`

Reads the object identified by a 40-character SHA-1, decompresses it, removes
the `<type> <size>\0` object header, and prints the remaining payload. The
handler describes the argument as a blob SHA, but the underlying reader only
requires a valid object hash and can return the payload of any object type.

### `hash-object -w <file>`

Reads a file, constructs `blob <byte-length>\0` followed by the file bytes,
computes the SHA-1 of that complete representation, compresses it with zlib,
and writes it under `.git/objects/<first-two>/<remaining-38>`. It prints the
resulting 40-character hash.

### `ls-tree --name-only <tree-sha>`

Reads and decompresses a tree object. Each tree entry is parsed as:

```text
<mode> <name>\0<20-byte binary object id>
```

The command discards the mode and binary object ID and prints each entry name.

### `write-tree`

Recursively snapshots the current working directory into Git tree and blob
objects. Entries are sorted by name for deterministic tree content. `.git` is
skipped. Directories are represented with mode `40000`; all non-directory
entries are read as regular files and represented with mode `100644`.

For each file, `writeBlob` stores a blob and returns its SHA-1. For each
directory, `writeTree` first creates child objects, then encodes its entries,
prepends `tree <byte-length>\0`, hashes the complete object, and stores it. The
top-level tree SHA is printed.

### `commit-tree <tree-sha> -p <parent-sha> -m <message>`

Creates a commit object referring to the supplied tree and parent. Its payload
contains a `parent` line, an `author` line, a `committer` line, a blank line,
and the supplied message followed by a newline. The author and committer names
and email addresses are fixed in the source, and both timestamps use the
current time with the local numeric timezone offset.

The complete `commit <byte-length>\0<payload>` representation is SHA-1 hashed,
zlib-compressed, and stored in the object database. The new commit SHA is
printed. This command does not update a branch reference.

### `clone <url> <directory>`

Cloning follows these stages:

1. Create the target directory, change the process working directory to it,
   and run `init`.
2. Request `<url>/info/refs?service=git-upload-pack` and parse its pkt-line
   advertisement. The service announcement and flush packets are ignored.
3. Select a SHA from `HEAD`, `refs/heads/master`, or `refs/heads/main`.
4. POST a `want <sha> side-band-64k` request and a `done` packet to
   `<url>/git-upload-pack`.
5. Remove upload-pack protocol framing and side-band progress messages from
   the response, retaining the packfile bytes.
6. Parse and write the packfile objects.
7. Read the selected commit, find its root tree, and recursively write the
   committed files into the cloned directory.

The clone operation does not create or update a local branch reference, and it
does not restore the caller's original working directory after `os.Chdir`.

## Object Database

Git objects are content-addressed. The SHA-1 is computed over the complete
uncompressed object, including its header:

```text
<type> <size>\0<payload>
```

`compressAndWriteObject` creates `.git/objects/<first-two>` and writes the
complete object through a zlib writer. `decompressObject` validates the hash
length, opens the corresponding path, decompresses it, and returns the full
object including its header. The higher-level readers split at the first null
byte to obtain the payload.

## Smart HTTP and Pkt-Line Handling

`readPktLine` reads four hexadecimal bytes, interprets the value as the total
packet length including those four bytes, and reads the remaining payload.
Length `0000` is represented as an empty packet. It is used while reading the
reference advertisement.

For upload-pack, `fetchPackfile` constructs the request body manually. It sends
one `want` packet, a flush packet, and one `done` packet. The response is read
in full and then scanned as pkt-lines. `NAK` packets are ignored. Side-band
channel 1 contributes pack data, channel 2 is printed as remote progress, and
channel 3 becomes an error.

The discovered capability text is recorded while parsing advertisement lines,
but the request uses the fixed capability `side-band-64k` rather than
negotiating capabilities dynamically.

## Packfile Parsing and Delta Resolution

`parseAndWritePackfile` expects the pack data to begin with `PACK`, followed by
the version and a four-byte big-endian object count. Each object header uses
Git's variable-length size encoding.

The parser uses two phases:

1. Base objects of types commit, tree, blob, and tag are decompressed from the
   pack, given their normal Git headers, hashed, and stored. `REF_DELTA`
   objects are retained with their 20-byte base SHA and delta instructions.
2. Pending deltas are revisited repeatedly. A delta is applied once its base
   object can be read from `.git/objects`. If a pass resolves no delta while
   unresolved deltas remain, processing stops with a missing-base error.

`OFS_DELTA` objects are explicitly unsupported.

`applyDelta` first reads the base object's payload, then reads the delta's
source and target sizes. It executes insert instructions directly from the
delta stream and copy instructions using the variable offset and size bit
fields. A zero copy size means `0x10000`. The reconstructed payload receives
the original base object type and a newly calculated size header before being
hashed and stored.

## Checkout

`checkout` loads a commit object, finds its first `tree <sha>` header line,
and delegates to `checkout_tree`. Tree parsing uses `bufio.Reader` to read the
mode, name, and exactly 20 bytes of binary object ID for each entry.

Directories with mode `40000` are created recursively. Other entries are
treated as blobs. Blob headers are removed before writing file contents. Mode
`100755` produces an executable file with permissions `0755`; all other files
use `0644`.

## Current Scope and Limitations

- Only the command forms listed above are accepted.
- Object IDs are expected to be full 40-character SHA-1 values.
- The local implementation does not maintain the index, refs, branch state,
  configuration, reflogs, or a working-tree status model.
- `write-tree` treats every non-directory entry as a regular file and skips
  only `.git`.
- Clone supports `REF_DELTA` but not `OFS_DELTA` pack entries.
- Clone selects one advertised commit and checks it out; it does not record a
  remote-tracking branch or local branch.
- The implementation relies on the current process working directory for all
  `.git` access and changes that directory during cloning.
