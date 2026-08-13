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
	"path/filepath"
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
	packData, err := fetchPackfile(url, wantedSHA)
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

	fmt.Println("Checking out files...")
    if err := checkout(wantedSHA, targetDir); err != nil {
		fmt.Printf("Error during checkout: %v\n", err)
		return err
    }
    fmt.Println("Checkout complete!")
	
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

func fetchPackfile(repoURL, wantedSHA string) ([]byte, error) {
	capabilities := "side-band-64k"
	wantLine := fmt.Sprintf("want %s %s\n", wantedSHA, capabilities)
    doneLine := "done\n"
    wantPkt := fmt.Sprintf("%04x%s", len(wantLine)+4, wantLine)
    donePkt := fmt.Sprintf("%04x%s", len(doneLine)+4, doneLine)
    body := wantPkt + "0000" + donePkt
	
	// fmt.Printf("Request body (hex):\n%x\n", body)
	// fmt.Printf("Request body (string):\n%q\n", body)

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

	// fmt.Printf("Response status: %s\n", resp.Status)
	// fmt.Printf("Content-Type: %s\n", resp.Header.Get("Content-Type"))

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
				fmt.Printf("remote: %s\n", string(payload[1:]))
				continue
			case 3:
				return nil, fmt.Errorf("remote error: %s", string(payload[1:]))
			}
		}
		packData = append(packData, payload...)
	}
	return packData, nil
}

// func readOffset(r io.Reader) (uint64, error) {
//     var offset uint64
//     var b [1]byte
//     for {
//         if _, err := r.Read(b[:]); err != nil {
//             return 0, err
//         }

//         offset = (offset + 1) << 7
//         offset |= uint64(b[0] & 0x7f)

//         if b[0]&0x80 == 0 {
//             break
//         }
//     }
//     return offset, nil
// }

func applyDelta(referenceHashHex string, deltaData []byte) ([]byte, error) {
	// 1. Fetch the base object that this delta modifies
	baseFullContent, err := decompressObject(referenceHashHex)
	if err != nil {
		return nil, fmt.Errorf("could not read base object %s for delta: %v", referenceHashHex, err)
	}

	//decompressObject returns the git header too
	// must separate the header from the actual base data
	parts := bytes.SplitN(baseFullContent, []byte{0}, 2)
	if len(parts) < 2 {
		return nil, errors.New("invalid base object format")
	}
	baseHeader := parts[0]
	baseData := parts[1]
	deltaBuffer := bytes.NewBuffer(deltaData)
	
	// Read Source and target lengths (var-len int)
	_, err = readDeltaSize(deltaBuffer) // Source length
	if err != nil { return nil, err}

	targetLength, err := readDeltaSize(deltaBuffer)
	if err != nil { return nil, err}

	var targetData []byte

	//Process the Insert/Copy commands
	for deltaBuffer.Len() > 0 {
		command, _ := deltaBuffer.ReadByte()
		
		if command&0x80 == 0 {
			//INSERT command
			insertLen := int(command & 0x7f)
			insertData := make([]byte, insertLen)
			deltaBuffer.Read(insertData)
			targetData = append(targetData, insertData...)
		} else {
			//COPY command
			offset := uint32(0)
			for i := 0; i < 4; i++ {
				if command&(1<<i) != 0 {
					b, _ := deltaBuffer.ReadByte()
					offset |= uint32(b) << (8*i)
				}
			}
			size := uint32(0)
			for i := 0; i < 3; i++ {
				if command&(0b10000<<i) != 0 {
					b, _ := deltaBuffer.ReadByte()
					size |= uint32(b) << (8*i)
				}
			}
			if size == 0 {
				size = 0x10000
			}
			targetData = append(targetData, baseData[offset:offset+size]...)
		}
	}
	if len(targetData) != int(targetLength) {
		return nil, fmt.Errorf("target data length mismatch")
	}
	// Reattach the header
	objTypeStr := strings.Split(string(baseHeader), " ")[0]
	finalHeader := fmt.Appendf(nil, "%s %d\x00", objTypeStr, len(targetData))
	return append(finalHeader, targetData...), nil
}

func readDeltaSize(r *bytes.Buffer) (uint64, error) {
	var size uint64
	var shift uint 
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		size |= uint64(b&0x7f) << shift
		shift += 7
        if b&0x80 == 0 {
			break
		}
	}
	return size, nil
}

// Create a struct to hold deltas that are waiting for their base objects
type pendingDelta struct {
	baseShaHex string
	deltaData  []byte
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

	// Slice to queue deltas we can't process yet
	var delayedDeltas []pendingDelta

	// --- PASS 1: Extract base objects and queue deltas ---
	for i := 0; i < int(objCount); i++ {
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

		if objType == 6 { 
			return errors.New("OFS_DELTA not supported in this implementation")
		}

		var baseShaHex string
		if objType == 7 { // REF_DELTA
			baseSha := make([]byte, 20)
			if _, err := r.Read(baseSha); err != nil {
				return err
			}
			baseShaHex = fmt.Sprintf("%x", baseSha)
		}

		zlibReader, err := zlib.NewReader(r)
		if err != nil {
			return err
		}
		objData, err := io.ReadAll(zlibReader)
		zlibReader.Close()
		if err != nil {
			return err
		}

		if objType >= 1 && objType <= 4 {
			// Map the integer type to the Git string type
			var typeStr string
			switch objType {
			case 1:
				typeStr = "commit"
			case 2:
				typeStr = "tree"
			case 3:
				typeStr = "blob"
			case 4:
				typeStr = "tag"
			}

			// Construct the Git object header: "<type> <length>\x00"
			header := fmt.Sprintf("%s %d\x00", typeStr, len(objData))
			
			// Combine the header and the raw data
			fullContent := append([]byte(header), objData...)

			// Hash the FULL content (header + data) to get the correct Git SHA
			sha := fmt.Sprintf("%x", sha1.Sum(fullContent))
			
			// Write the full content (which includes the header) to disk
			if err := compressAndWriteObject(sha, fullContent); err != nil {
				return err
			}
		} else if objType == 7 {
			// It's a delta. Queue it up for Pass 2.
			delayedDeltas = append(delayedDeltas, pendingDelta{
				baseShaHex: baseShaHex,
				deltaData:  objData,
			})
		}
	}

	// --- PASS 2: Resolve deltas (Multi-pass for delta chains) ---
	unresolvedCount := len(delayedDeltas)
	for unresolvedCount > 0 {
		resolvedInThisPass := 0
		var nextPassDeltas []pendingDelta

		for _, pDelta := range delayedDeltas {
			// Check if the base object is on disk yet
			// decompressObject will throw an error if the file is missing
			_, err := decompressObject(pDelta.baseShaHex)
			
			if err != nil {
				// Base doesn't exist yet, push it to the next pass
				nextPassDeltas = append(nextPassDeltas, pDelta)
				continue
			}

			// The base exists! We can safely apply the delta.
			targetData, err := applyDelta(pDelta.baseShaHex, pDelta.deltaData)
			if err != nil {
				return fmt.Errorf("failed applying delta to %s: %v", pDelta.baseShaHex, err)
			}

			sha := fmt.Sprintf("%x", sha1.Sum(targetData))
			if err := compressAndWriteObject(sha, targetData); err != nil {
				return err
			}
			resolvedInThisPass++
		}

		// If we loop through all pending deltas and resolve 0 of them, 
		// we are stuck in an infinite loop due to a missing base object.
		if resolvedInThisPass == 0 && len(nextPassDeltas) > 0 {
			return errors.New("delta resolution stalled: missing base objects")
		}

		// Set up the slice for the next while-loop iteration
		delayedDeltas = nextPassDeltas
		unresolvedCount = len(delayedDeltas)
	}

	return nil
}

func checkout(commitSha string, targetDir string) error {
	//1. Get the commit object
	commitData, err := decompressObject(commitSha)
	if err != nil {
		return fmt.Errorf("failed to read commit object: %v", err)
	}

	//2. Parse the commit to find the root tree
	parts := bytes.SplitN(commitData, []byte{0}, 2)
	if len(parts) < 2 {
		return errors.New("invalid commit object format")
	}
	body := string(parts[1])

	var treeSha string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "tree ") {
			treeSha = strings.TrimPrefix(line, "tree ")
			break
		}
	}
	if treeSha == "" {
		return errors.New("could not find tree in commit")
	}
	// 3. Kick off the recursive tree writing
	return checkout_tree(treeSha, targetDir)
}

func checkout_tree(treeShaHex string, basePath string) error {
	treeData, err := decompressObject(treeShaHex)
	if err != nil {
		return fmt.Errorf("failed to read tree object %s: %v", treeShaHex, err)
	}

	// Strip the "tree <size>\x00" header
	parts := bytes.SplitN(treeData, []byte{0}, 2)
	if len(parts) < 2 {
		return errors.New("invalid tree object format")
	}
	content := parts[1]

	// Parse the tree entries
	// Tree entry format: "<mode> <name>\x00<20-byte-sha>"
	
	// Wrap the bytes.Reader in a bufio.Reader to get access to ReadBytes
	reader := bufio.NewReader(bytes.NewReader(content))
	
	for {
		// Read the mode (permissions) up to the space character
		modeBytes, err := reader.ReadBytes(' ')
		if err == io.EOF {
			break // We've reached the end of the tree content, break the loop!
		}
		if err != nil {
			return err
		}
		modeStr := strings.TrimSpace(string(modeBytes))

		// Read the file/folder name up to the null byte
		nameBytes, err := reader.ReadBytes(0)
		if err != nil {
			return err
		}
		nameStr := strings.TrimRight(string(nameBytes), "\x00")

		// Read the 20-byte binary SHA
		shaBytes := make([]byte, 20)
		// Use io.ReadFull to ensure we grab exactly 20 bytes for the SHA
		if _, err := io.ReadFull(reader, shaBytes); err != nil {
			return err
		}
		entryShaHex := fmt.Sprintf("%x", shaBytes)

		// Determine the full path to write to
		entryPath := filepath.Join(basePath, nameStr)

		if modeStr == "40000" { 
			// It's a directory (Tree)
			if err := os.MkdirAll(entryPath, 0755); err != nil {
				return err
			}
			// Recurse into the sub-directory
			if err := checkout_tree(entryShaHex, entryPath); err != nil {
				return err
			}
		} else { 
			// It's a file (Blob)
			blobData, err := decompressObject(entryShaHex)
			if err != nil {
				return err
			}
			
			// Strip the "blob <size>\x00" header
			blobParts := bytes.SplitN(blobData, []byte{0}, 2)
			if len(blobParts) < 2 {
				return errors.New("invalid blob format")
			}
			
			// Handle standard files vs executable files
			perm := os.FileMode(0644)
			if modeStr == "100755" {
				perm = 0755 
			}
			
			// Write the actual file content to disk
			if err := os.WriteFile(entryPath, blobParts[1], perm); err != nil {
				return err
			}
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
