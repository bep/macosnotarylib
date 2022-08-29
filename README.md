[![Tests on Linux, MacOS and Windows](https://github.com/bep/macosnotarylib/workflows/Test/badge.svg)](https://github.com/bep/macosnotarylib/actions?query=workflow:Test)
[![Go Report Card](https://goreportcard.com/badge/github.com/bep/macosnotarylib)](https://goreportcard.com/report/github.com/bep/macosnotarylib)
[![GoDoc](https://godoc.org/github.com/bep/macosnotarylib?status.svg)](https://godoc.org/github.com/bep/macosnotarylib)


This notarizes files using Apple's [Notary API](https://developer.apple.com/documentation/notaryapi), which means that it can run on any OS.

Note that the archived binary must already be signed, see [testdata/sign.sh](testdata/sign.sh), which unortunate is harder to do outside of a Macintosh.

See the single test for a "how to use". Running that prints something ala:

```bash
2022/08/30 09:59:15 Submitting helloworld.zip with checksum a53c8738fdd28a3558057c8825f633860846773baae89cf3e0e36f12896393af
2022/08/30 09:59:20 Successfully uploaded file to S3 location https://notary-submissions-prod.s3.us-west-2.amazonaws.com/prod/AROARQRX7CZS3PRF6ZA5L%3A09fac26e-68b5-4e73-b6d8-2edf1e61dee9
2022/08/30 09:59:31 [1] Checking status of 09fac26e-68b5-4e73-b6d8-2edf1e61dee9
2022/08/30 09:59:43 [2] Checking status of 09fac26e-68b5-4e73-b6d8-2edf1e61dee9
--- PASS: TestNotarizeZip (28.65s)
```