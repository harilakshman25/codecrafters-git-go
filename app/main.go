package main

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mygit <command> [<args>...]\n")
		os.Exit(1)
	}

	command := os.Args[1]
	var err error

	switch command {
	case "init":
		err = handleInit()
	case "cat-file":
		err = handleCatFile(os.Args[2:])
	case "hash-object":
		err = handleHashObject(os.Args[2:])
	case "ls-tree":
		err = handleLsTree(os.Args[2:])
	case "write-tree":
		err = handleWriteTree()
	case "commit-tree":
		err = handleCommitTree(os.Args[2:])
	case "clone":
		err = handleClone(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %s\n", command)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleInit() error {
	dirs := []string{
		".git",
		".git/objects",
		".git/refs",
		".git/refs/heads",
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	headContent := []byte("ref: refs/heads/master\n")
	if err := os.WriteFile(".git/HEAD", headContent, 0644); err != nil {
		return err
	}

	fmt.Println("Initialized git directory")
	return nil
}

func handleCatFile(args []string) error {
	if len(args) < 2 || args[0] != "-p" {
		return errors.New("usage: mygit cat-file -p <blob_sha>")
	}

	sha := args[1]
	data, err := readBlob(sha)
	if err != nil {
		return err
	}

	fmt.Print(string(data))
	return nil
}

func handleHashObject(args []string) error {
	if len(args) < 2 || args[0] != "-w" {
		return errors.New("usage: mygit hash-object -w <file>")
	}

	filePath := args[1]
	content, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	sha, err := writeBlob(content)
	if err != nil {
		return err
	}

	fmt.Println(sha)
	return nil
}

func handleLsTree(args []string) error {
	if len(args) < 2 || args[0] != "--name-only" {
		return errors.New("usage: mygit ls-tree --name-only <tree_sha>")
	}

	sha := args[1]
	entries, err := readTree(sha)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		fmt.Println(entry)
	}
	return nil
}

func handleWriteTree() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	sha, err := writeTree(cwd)
	if err != nil {
		return err
	}

	fmt.Println(sha)
	return nil
}

func handleCommitTree(args []string) error {
	if len(args) < 5 || args[1] != "-p" || args[3] != "-m" {
		return errors.New("usage: mygit commit-tree <tree-sha> -p <commit_sha> -m <msg>")
	}
	sha, err := commitTree(args[0], args[2], args[4])
	if err != nil {
		return err
	}
	fmt.Println(sha)
	return nil
}

func readBlob(sha string) ([]byte, error) {
	fullContent, err := decompressObject(sha)
	if err != nil {
		return nil, err
	}

	parts := bytes.SplitN(fullContent, []byte{0}, 2)
	if len(parts) < 2 {
		return nil, errors.New("invalid object format")
	}

	return parts[1], nil
}

func writeBlob(content []byte) (string, error) {
	header := fmt.Appendf(nil, "blob %d\x00", len(content))
	fullData := append(header, content...)

	sha := fmt.Sprintf("%x", sha1.Sum(fullData))

	err := compressAndWriteObject(sha, fullData)
	if err != nil {
		return "", err
	}

	return sha, nil
}

func readTree(sha string) ([]string, error) {
	fullContent, err := decompressObject(sha)
	if err != nil {
		return nil, err
	}

	nullIndex := bytes.IndexByte(fullContent, 0)
	if nullIndex == -1 {
		return nil, errors.New("invalid tree object header")
	}

	data := fullContent[nullIndex+1:]
	var names []string

	for len(data) > 0 {
		nullIdx := bytes.IndexByte(data, 0)
		if nullIdx == -1 {
			break
		}

		modeAndName := data[:nullIdx]
		parts := bytes.SplitN(modeAndName, []byte{' '}, 2)
		if len(parts) == 2 {
			names = append(names, string(parts[1]))
		}

		data = data[nullIdx+1+20:]
	}

	return names, nil
}

func writeTree(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var treeEntries []byte
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}

		fullPath := path.Join(dir, name)
		var mode string
		var shaHex string

		if entry.IsDir() {
			mode = "40000"
			shaHex, err = writeTree(fullPath)
		} else {
			mode = "100644"
			content, err := os.ReadFile(fullPath)
			if err != nil {
				return "", err
			}
			shaHex, err = writeBlob(content)
		}

		if err != nil {
			return "", err
		}

		shaBinary, err := hex.DecodeString(shaHex)
		if err != nil {
			return "", err
		}

		entryLine := fmt.Appendf(nil, "%s %s\x00", mode, name)
		entryLine = append(entryLine, shaBinary...)
		treeEntries = append(treeEntries, entryLine...)
	}

	header := fmt.Appendf(nil, "tree %d\x00", len(treeEntries))
	fullContent := append(header, treeEntries...)
	sha := fmt.Sprintf("%x", sha1.Sum(fullContent))
	err = compressAndWriteObject(sha, fullContent)
	return sha, err
}

func commitTree(tree_sha string, commit_sha string, commit_msg string) (string, error) {
	data := fmt.Appendf(nil, "parent %s\n", commit_sha)
	_, offset := time.Now().Zone()
	data = fmt.Appendf(data, "author Hari <harilakshman24@gmail.com> %d +%d\n", time.Now().Unix(), offset)
	data = fmt.Appendf(data, "committer Lakshman <harilakshman509716@kgpian.iitkgp.ac.in> %d +%d\n\n", time.Now().Unix(), offset)
	data = fmt.Appendf(data, "%s\n", commit_msg)
	header := fmt.Appendf(nil, "commit %d\x00tree %s\n", len(data), tree_sha)
	content := append(header, data...)
	sha := fmt.Sprintf("%x", sha1.Sum(content))
	err := compressAndWriteObject(sha, content)
	return sha, err
}

func handleClone(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: mygit clone <url> <dir>")
	}
	url := args[0]
	targetDir := args[1]

	//1. Init
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}
	if err := os.Chdir(targetDir); err != nil {
		return err
	}
	if err := handleInit(); err != nil {
		return err
	}

	// 2. Discovery
	resp, err := http.Get(url + "/info/refs?service=git-upload-pack")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Read the entire response as a pkt-line stream
	var wantedSHA string
	var capabilities string
	reader := bufio.NewReader(resp.Body)
	for {
		data, err := readPktLine(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if len(data) == 0 {
			continue //flush
		}
		//The first line might be "# service=.." - skip it
		if bytes.HasPrefix(data, []byte("# service")) {
			continue
		}

		parts := bytes.SplitN(data, []byte{0}, 2)
		refLine := parts[0]
		if len(parts) > 1 {
			capabilities = string(parts[1])
			capabilities = strings.TrimSpace(capabilities)
		}
		//Format: "<sha> <refname>\n"
		fields := bytes.Fields(refLine)
		if len(fields) >= 2 {
			sha := string(fields[0])
			ref := string(fields[1])
			if ref == "HEAD" || ref == "refs/heads/master" || ref == "refs/heads/main" {
				wantedSHA = sha
				// continue reading all
			}
		}
	}

	if wantedSHA == "" {
		return errors.New("could not find HEAD or master SHA")
	}
	fmt.Printf("Found SHA: %s\n", wantedSHA)
	fmt.Printf("Capabilities: %s\n", capabilities)

	capabilities = strings.TrimSuffix(capabilities, "\n")

	// 3. Fetch packfile
	packData, err := fetchPackfile(url, wantedSHA, capabilities)
	if err != nil {
		return err
	}
	fmt.Printf("Recieved packfile of %d bytes\n", len(packData))

	// 4. Parse and write objects
	if err := parseAndWritePackfile(packData); err != nil {
		return err
	}

	// 5. Checkout: we need to read the commit tree and write files.
	// For now, we can just print success.
	fmt.Println("Clone completed (objects downloaded).")
	return nil
}

func readPktLine(r io.Reader) ([]byte, error) {
	var lenHex [4]byte
	if _, err := io.ReadFull(r, lenHex[:]); err != nil {
		return nil, err
	}
	length, err := strconv.ParseInt(string(lenHex[:]), 16, 32)
	if err != nil {
		return nil, err
	}
	if length == 0 {
		return []byte{}, nil //flush packet
	}
	// length includes the 4 bytes of the length prefix itself
	dataLen := length - 4
	if dataLen < 0 {
		return nil, errors.New("invalid pkt-line length")
	}
	data := make([]byte, dataLen)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	return data, nil
}

func fetchPackfile(repoURL, wantedSHA, capabilities string) ([]byte, error) {
	wantLine := fmt.Sprintf("want %s %s\n", wantedSHA, capabilities)
	doneLine := "done\n"
	wantPkt := fmt.Sprintf("%04x%s", len(wantLine)+4, wantLine)
	donePkt := fmt.Sprintf("%04x%s", len(doneLine)+4, doneLine)
	body := wantPkt + "0000" + donePkt

	fmt.Printf("Request body (hex):\n%x\n", body)
	fmt.Printf("Request body (string):\n%q\n", body)

	req, err := http.NewRequest("POST", repoURL+"/git-upload-pack", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	req.Header.Set("Accept", "application/x-git-upload-pack-result")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	fmt.Printf("Response status: %s\n", resp.Status)
	fmt.Printf("Content-Type: %s\n", resp.Header.Get("Content-Type"))

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var packData []byte
	for i := 0; i < len(raw); {
		if i+4 > len(raw) {
			return nil, errors.New("truncated pkt-line in upload-pack response")
		}

		length, err := strconv.ParseInt(string(raw[i:i+4]), 16, 32)
		if err != nil {
			return nil, err
		}
		i += 4
		if length == 0 {
			continue
		}
		if length < 4 {
			return nil, errors.New("invalid pkt-line length")
		}

		payload := raw[i : i+int(length)-4]
		i += int(length) - 4

		if bytes.Equal(payload, []byte("NAK\n")) {
			continue
		}
		if len(payload) > 0 {
			switch payload[0] {
			case 1:
				payload = payload[1:]
			case 2:
				fmt.Fprintf(os.Stderr, "remote: %s\n", string(payload[1:]))
				continue
			case 3:
				return nil, fmt.Errorf("remote error: %s", string(payload[1:]))
			}
		}
		packData = append(packData, payload...)
	}
	return packData, nil
}

func readOffset(r io.Reader) (uint64, error) {
	var offset uint64
	var b [1]byte
	for {
		if _, err := r.Read(b[:]); err != nil {
			return 0, err
		}
		offset = (offset << 7) | uint64(b[0]&0x7f)
		if b[0]&0x80 == 0 {
			break
		}
		offset++
	}
	return offset, nil
}

func parseAndWritePackfile(packData []byte) error {
    r := bytes.NewReader(packData)

    var magic [4]byte
    if _, err := r.Read(magic[:]); err != nil || string(magic[:]) != "PACK" {
        return errors.New("invalid packfile magic")
    }

    var version [4]byte
    if _, err := r.Read(version[:]); err != nil {
        return err
    }

    var numObj [4]byte
    if _, err := r.Read(numObj[:]); err != nil {
        return err
    }
    objCount := binary.BigEndian.Uint32(numObj[:])
    fmt.Printf("Packfile contains %d objects\n", objCount)

    for i := 0; i < int(objCount); i++ {
        // Read object header: type and size
        firstByte, err := r.ReadByte()
        if err != nil {
            return err
        }
        objType := (firstByte >> 4) & 0x07
        size := uint64(firstByte & 0x0f)
        shift := 4
        for firstByte&0x80 != 0 {
            b, err := r.ReadByte()
            if err != nil {
                return err
            }
            size |= uint64(b&0x7f) << shift
            shift += 7
            firstByte = b
        }

       // --- Handle delta base info before zlib ---
        if objType == 7 { // REF_DELTA (Type 7): 20-byte base SHA
            baseSha := make([]byte, 20)
            if _, err := r.Read(baseSha); err != nil {
                return err
            }
        } else if objType == 6 { // OFS_DELTA (Type 6): variable-length offset
            if _, err := readOffset(r); err != nil {
                return err
            }
        }

        // Now read the compressed object data
        zlibReader, err := zlib.NewReader(r)
        if err != nil {
            return err
        }
        objData, err := io.ReadAll(zlibReader)
        zlibReader.Close()
        if err != nil {
            return err
        }

        // Only write full objects (commit, tree, blob, tag)
        // Delta objects (type 6,7) are skipped for now.
        if objType >= 1 && objType <= 4 {
            sha := fmt.Sprintf("%x", sha1.Sum(objData))
            if err := compressAndWriteObject(sha, objData); err != nil {
                return err
            }
        } else {
            // Delta or other types – ignore (or store for later resolution)
            // If you want to handle deltas fully, you'd need to resolve them.
            // fmt.Fprintf(os.Stderr, "Skipping object type %d\n", objType)
        }
    }
    return nil
}

func decompressObject(sha string) ([]byte, error) {
	if len(sha) != 40 {
		return nil, errors.New("invalid hash length")
	}

	objectPath := path.Join(".git", "objects", sha[:2], sha[2:])
	file, err := os.Open(objectPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	zlibReader, err := zlib.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer zlibReader.Close()

	return io.ReadAll(zlibReader)
}

func compressAndWriteObject(sha string, data []byte) error {
	objectDir := path.Join(".git", "objects", sha[:2])
	if err := os.MkdirAll(objectDir, 0755); err != nil {
		return err
	}

	objectPath := path.Join(objectDir, sha[2:])
	file, err := os.Create(objectPath)
	if err != nil {
		return err
	}
	defer file.Close()

	zlibWriter := zlib.NewWriter(file)
	if _, err := zlibWriter.Write(data); err != nil {
		return err
	}
	return zlibWriter.Close()
}
