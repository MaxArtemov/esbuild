package my_helpers

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
)

func ReplaceFileContents(toPath string, fromPath string) error {
	contents, readError := os.ReadFile(fromPath)
	if readError != nil {
		return readError
	}
	err := os.WriteFile(toPath, contents, 0644)
	return err
}

func HashString(input string) string {
	hasher := sha256.New()
	hasher.Write([]byte(input))
	hashBytes := hasher.Sum(nil)
	hashString := hex.EncodeToString(hashBytes)
	return hashString
}
