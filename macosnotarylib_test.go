package macosnotarylib

import (
	"log"
	"os"
	"testing"

	qt "github.com/frankban/quicktest"
	"github.com/golang-jwt/jwt/v4"
)

// Run with  go test -v . to see the log output.
func TestNotarizeZip(t *testing.T) {
	if os.Getenv("CI") != "" {
		// TODO(bep)
		t.Skip("Skipping test in CI")
	}
	c := qt.New(t)

	issuerID := os.Getenv("MACOSNOTARYLIB_ISSUER_ID")
	c.Assert(issuerID, qt.Not(qt.Equals), "")
	kid := os.Getenv("MACOSNOTARYLIB_KID")
	c.Assert(kid, qt.Not(qt.Equals), "")
	// This test also depends on the private key from env MACOSNOTARYLIB_PRIVATE_KEY in base64 format. See below.

	n, err := New(
		Options{
			InfoLoggerf: func(format string, a ...interface{}) {
				log.Printf(format, a...)
			},
			IssuerID: issuerID,
			Kid:      kid,
			SignFunc: func(token *jwt.Token) (string, error) {
				key, err := LoadPrivateKeyFromEnvBase64("MACOSNOTARYLIB_PRIVATE_KEY")
				c.Assert(err, qt.IsNil)
				return token.SignedString(key)
			},
		},
	)

	c.Assert(err, qt.IsNil)

	err = n.Submit("testdata/helloworld.zip")
	c.Assert(err, qt.IsNil)

}
