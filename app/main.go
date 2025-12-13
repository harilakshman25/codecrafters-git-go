package main

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"os"
	"strings"
	"path"
	"io"
)

// Usage: your_program.sh <command> <arg1> <arg2> ...
func main() {
	// You can use print statements as follows for debugging, they'll be visible when running tests.
	fmt.Fprintf(os.Stderr, "Logs from your program will appear here!\n")

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: mygit <command> [<args>...]\n")
		os.Exit(1)
	}

	switch command := os.Args[1]; command {
	case "init":
		initRepo("")
		fmt.Println("Initialized git directory")
	case "cat-file":
		if len(os.Args) < 4 {
			handleError(errors.New("usage: mygit cat-file -p [<args>...]"))
			os.Exit(1)
		}

		if os.Args[2] != "-p" {
           handleError(errors.New("usage: mygit cat-file -p [<args>...]"))
		   os.Exit(1)
		}

		content, err := readContentObject(os.Args[3])
		if err != nil {
			handleError(err)
			return
		}

		fmt.Print(content)

	default:
		fmt.Fprintf(os.Stderr, "Unknown command %s\n", command)
		os.Exit(1)
	}
}

func initRepo(repoPath string) {
	for _, dir := range []string{".git", ".git/objects", ".git/refs", ".git/refs/heads"} {
		dirPath := path.Join(repoPath, dir)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Error creating directory: %s\n", err)
		}
	}

	headFileContents := []byte("ref: refs/heads/master\n")
	if err := os.WriteFile(".git/HEAD", headFileContents, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing file: %s\n", err)
	}
}

func readContentObject(hash string) (string, error) {
	if len(hash) != 40 {
		return "", fmt.Errorf("invalid len of the hash")
	}

	buf := readObject(hash)
	parts := strings.SplitN(buf.String(), "\x00", 2)
	if len(parts) != 2 {
       return "", fmt.Errorf("invalid object")
	}
	return parts[1], nil
}

func readObject(hash string) bytes.Buffer {
    dir := fmt.Sprintf(".git/objects/%s", hash[:2])
	fileName := fmt.Sprintf("%s/%s", dir, hash[2:])

	fileContent, err := os.ReadFile(fileName) 
	if err != nil {
		fmt.Fprintf(os.Stderr, "read file got err=%v", err)
		os.Exit(1)
	}

    rcloser, err := zlib.NewReader(bytes.NewReader(fileContent))
	if err != nil {
		fmt.Fprintf(os.Stderr, "zlib new reader got err=%v", err)
		os.Exit(1)
	}

	defer rcloser.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rcloser); err != nil {
		fmt.Fprintf(os.Stderr, "read file to buffer got err=%v\n", err)
		os.Exit(1)
	}

	return buf

}



func handleError(err error) {
	fmt.Fprint(os.Stderr, err.Error()+"\n")
}