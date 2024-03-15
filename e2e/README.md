# CCM e2e test

This package holds end-to-end tests to verify CCM functionality for a set of supported Kubernetes versions.

It may be run either locally or on the CI.

## Pre-requisite

You need to have [ginkgo](https://onsi.github.io/ginkgo/) installed and configured.

## How to run

To begin the test, the `run.sh` should be executed. There are required parameters to be passed to the script as follows:

- `-v | --version`: bizflycloud cluster's k8s version uid
- `-P | --vcp`: bizflycloud virtual private network's uid
- `-u | --app-cred-id`: bizflycloud app credential id (the script mainly use this authentication method)
- `-p | --app-cred-secret`: bizflycloud app credential secret (the script mainly use this authentication method)
- `--email`: bizflycloud account email (this is used for bizflyctl to provision resources)
- `--password`: bizflycloud account password (this is used for bizflyctl to provision resources)
- `--image`: image of a ccm that would be used fro e2e testing
- `--token`: a secret token for patch test image to testing resource
