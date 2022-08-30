package macosnotarylib

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"

	"github.com/golang-jwt/jwt/v4"
)

const (
	apiSubmssions = "https://appstoreconnect.apple.com/notary/v2/submissions"
)

// New creates a new Notarizer. You can call Submit multiple time to submit multiple files,
// but the JWT token will eventually expire, default after 20 minutes.
func New(opts Options) (*Notarizer, error) {
	if opts.InfoLoggerf == nil {
		opts.InfoLoggerf = func(format string, a ...any) {}
	}

	if opts.SignFunc == nil {
		return nil, errors.New("SignFunc is required")
	}

	if opts.SubmissionTimeout == 0 {
		opts.SubmissionTimeout = 5 * time.Minute
	}

	if opts.TokenTimeout == 0 {
		opts.TokenTimeout = 20 * time.Minute
	}

	n := &Notarizer{
		infof: opts.InfoLoggerf,
		opts:  opts,
	}

	signature, err := n.createAndSignToken()
	if err != nil {
		return nil, err
	}

	n.signature = signature

	return n, nil
}

type Options struct {
	// InfoLogger will log information about the notarization process. No secrets.
	InfoLoggerf func(format string, a ...any)

	// Your issuer ID from the API Keys page in App Store Connect; for example, 57246542-96fe-1a63-e053-0824d011072a.
	IssuerID string

	// Your private key ID from App Store Connect.
	Kid string

	// Timeout waiting for the notarization to complete.
	// Defaults to 5 minutes.
	SubmissionTimeout time.Duration

	// The JWT signing token expires after this duration,
	// default is 20 minutes.
	TokenTimeout time.Duration

	// The signing function to use.
	// Return the result of token.SignedString(appStoreConnectPrivateKey)
	// where the private key is the one connected to the kid field.
	SignFunc func(token *jwt.Token) (string, error)
}

// LoadPrivateKeyFromEnvBase64 is a helper function to load a key from the environment in base64 format.
func LoadPrivateKeyFromEnvBase64(envKey string) (*ecdsa.PrivateKey, error) {
	keyBase64 := os.Getenv(envKey)
	if keyBase64 == "" {
		return nil, fmt.Errorf("%s is not set", envKey)
	}
	keyBytes, err := base64.StdEncoding.DecodeString(keyBase64)
	if err != nil {
		return nil, err
	}
	key, err := jwt.ParseECPrivateKeyFromPEM(keyBytes)
	if err != nil {
		return nil, err
	}
	return key, nil
}

// Notarizer is the main struct for notarizing files.
type Notarizer struct {
	signature string
	infof     func(format string, a ...any)
	opts      Options
}

// Submit submits a new notarization request.
func (n *Notarizer) Submit(filename string) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	var fileBuf bytes.Buffer

	defer f.Close()
	h := sha256.New()
	w := io.MultiWriter(h, &fileBuf)
	if _, err := io.Copy(w, f); err != nil {
		return err
	}

	checksum := hex.EncodeToString(h.Sum(nil))
	submissionName := filepath.Base(filename)

	n.infof("Submitting %s with checksum %s", submissionName, checksum)

	req := &submissionRequest{
		Sha256:         checksum,
		SubmissionName: submissionName,
	}

	var buf bytes.Buffer

	if err := json.NewEncoder(&buf).Encode(req); err != nil {
		return err
	}

	request, err := n.newAPIRequest("POST", apiSubmssions, &buf)
	if err != nil {
		return err
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return errors.New(response.Status)
	}

	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	var resp submissionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return err
	}

	attrs := resp.Data.Attributes
	s3Config := &aws.Config{
		Region:      aws.String("us-west-2"),
		Credentials: credentials.NewStaticCredentials(attrs.AwsAccessKeyID, attrs.AwsSecretAccessKey, attrs.AwsSessionToken),
	}
	session, err := session.NewSession(s3Config)
	if err != nil {
		return err
	}
	uploader := s3manager.NewUploader(session)
	input := &s3manager.UploadInput{
		Bucket:      aws.String(attrs.Bucket),
		Key:         aws.String(attrs.Object),
		Body:        &fileBuf,
		ContentType: aws.String("application/zip"),
	}

	output, err := uploader.UploadWithContext(context.Background(), input)
	if err != nil {
		return err
	}

	n.infof("Successfully uploaded file to S3 location %s", output.Location)

	ctx, cancel := context.WithTimeout(context.Background(), n.opts.SubmissionTimeout)
	defer cancel()

	var (
		done  bool
		count int
	)

	for !done {
		select {
		case <-ctx.Done():
			return errors.New("timeout waiting for notarize submission response")
		default:
			count++
			time.Sleep(time.Duration(10+count) * time.Second)
			var err error
			done, err = n.checkStatus(count, resp.Data.ID)
			if err != nil {
				return err
			}
			if done {
				n.infof("Notarization completed!")
			}
		}
	}

	return nil

}

// newAPIRequest creates a new API request with the JWT signature applied.
func (n *Notarizer) newAPIRequest(method, endpoint string, body io.Reader) (*http.Request, error) {
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+n.signature)
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	return request, nil

}

func (n *Notarizer) checkStatus(count int, id string) (bool, error) {
	n.infof("[%d] Checking status of %s", count, id)
	request, err := n.newAPIRequest("GET", apiSubmssions+"/"+id, nil)
	if err != nil {
		return false, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return false, err
	}

	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("failed to check status for ID %s: %s", id, response.Status)
	}

	defer response.Body.Close()
	var resp submissionStatusResponse
	if err := json.NewDecoder(response.Body).Decode(&resp); err != nil {
		return false, err
	}

	switch resp.Data.Attributes.Status {
	case "Accepted":
		return true, nil
	case "In Progress":
		return false, nil
	default:
		if err := n.printLogInfo(id); err != nil {
			log.Printf("error: failed to print logs: %s", err)
		}
		return false, fmt.Errorf("unexpected status: %s", resp.Data.Attributes.Status)

	}
}

// printLogInfo prints some information about where to download the logs from.
func (n *Notarizer) printLogInfo(id string) error {
	n.infof("Checking status of %s", id)
	request, err := n.newAPIRequest("GET", apiSubmssions+"/"+id+"/logs", nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch logs with ID %s: %s", id, response.Status)
	}

	defer response.Body.Close()
	var resp logsResponse
	if err := json.NewDecoder(response.Body).Decode(&resp); err != nil {
		return err
	}

	n.infof("Logs for %s can be found at %s", id, resp.Data.Attributes.DeveloperLogURL)

	return nil

}

func (n *Notarizer) createAndSignToken() (string, error) {
	exp := time.Now().Add(n.opts.TokenTimeout).UTC().Unix()
	iat := time.Now().UTC().Unix()

	method := jwt.SigningMethodES256
	tok := &jwt.Token{
		Header: map[string]interface{}{
			"alg": method.Alg(),
			"kid": n.opts.Kid,
			"typ": "JWT",
		},
		Claims: jwt.MapClaims{
			"iss": n.opts.IssuerID,
			// The token’s creation time, in UNIX epoch time; for example, 1528407600.
			"iat": iat,
			// The token’s expiration time in Unix epoch time.
			"exp": exp,
			// Audience.
			"aud": "appstoreconnect-v1",
			// A list of operations you want App Store Connect to allow for this token.
			"scope": []string{"/notary/v2"},
		},
		Method: method,
	}

	return n.opts.SignFunc(tok)

}

type logsResponse struct {
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			DeveloperLogURL string `json:"developerLogUrl"`
		} `json:"attributes"`
	} `json:"data"`
	Meta struct {
	} `json:"meta"`
}

type submissionRequest struct {
	Sha256         string `json:"sha256"`
	SubmissionName string `json:"submissionName"`
}

type submissionResponse struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			AwsAccessKeyID     string `json:"awsAccessKeyId"`
			AwsSecretAccessKey string `json:"awsSecretAccessKey"`
			AwsSessionToken    string `json:"awsSessionToken"`
			Bucket             string `json:"bucket"`
			Object             string `json:"object"`
		} `json:"attributes"`
	} `json:"data"`
	Meta struct {
	} `json:"meta"`
}

type submissionStatusResponse struct {
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			Status      string    `json:"status"`
			Name        string    `json:"name"`
			CreatedDate time.Time `json:"createdDate"`
		} `json:"attributes"`
	} `json:"data"`
	Meta struct {
	} `json:"meta"`
}
