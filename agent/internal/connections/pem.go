package connections

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
)

// The agent's own check of PEM text, which the schema cannot state. A
// refusal names the field and what was expected, and never quotes the text.
const (
	certificateMessage = "this is not PEM text holding one or more certificates"
	privateKeyMessage  = "this is not PEM text holding one unencrypted private key"
)

// pemBlocks splits text into PEM blocks, refusing anything around or between
// them but whitespace, which pem.Decode would otherwise skip.
func pemBlocks(text string) ([]*pem.Block, bool) {
	rest := []byte(text)
	var blocks []*pem.Block
	for {
		rest = bytes.TrimLeft(rest, " \t\r\n")
		if len(rest) == 0 {
			return blocks, len(blocks) > 0
		}
		if !bytes.HasPrefix(rest, []byte("-----BEGIN ")) {
			return nil, false
		}
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil || len(block.Headers) != 0 {
			return nil, false
		}
		blocks = append(blocks, block)
	}
}

// certificatesParse reports whether text is one or more PEM CERTIFICATE
// blocks, each an X.509 certificate.
func certificatesParse(text string) bool {
	blocks, ok := pemBlocks(text)
	if !ok {
		return false
	}
	for _, block := range blocks {
		if block.Type != "CERTIFICATE" {
			return false
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return false
		}
	}
	return true
}

// privateKeyParses reports whether text is exactly one unencrypted PEM
// private key: PKCS #8, PKCS #1 RSA or SEC 1 EC.
func privateKeyParses(text string) bool {
	blocks, ok := pemBlocks(text)
	if !ok || len(blocks) != 1 {
		return false
	}
	block := blocks[0]
	var err error
	switch block.Type {
	case "PRIVATE KEY":
		_, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		_, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		_, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return false
	}
	return err == nil
}
