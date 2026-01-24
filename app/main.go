package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
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
	header := []byte(fmt.Sprintf("blob %d\x00", len(content)))
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

		entryLine := []byte(fmt.Sprintf("%s %s\x00", mode, name))
		entryLine = append(entryLine, shaBinary...)
		treeEntries = append(treeEntries, entryLine...)
	}

	header := []byte(fmt.Sprintf("tree %d\x00", len(treeEntries)))
	fullContent := append(header, treeEntries...)
	sha := fmt.Sprintf("%x", sha1.Sum(fullContent))
	err = compressAndWriteObject(sha, fullContent)
	return sha, err
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
