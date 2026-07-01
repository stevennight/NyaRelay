package main

import (
	"fmt"
	"os"

	sharedcrypto "nyarelay/internal/shared/crypto"
)

func main() {
	publicKey, privateKey, err := sharedcrypto.GenerateSigningKey()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "generate update signing key: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("NYARELAY_UPDATE_PUBLIC_KEY=" + publicKey)
	fmt.Println("NYARELAY_UPDATE_SIGNING_KEY=" + privateKey)
}
