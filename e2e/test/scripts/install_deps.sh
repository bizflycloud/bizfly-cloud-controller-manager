# Install kubectl
curl -LfO https://storage.googleapis.com/kubernetes-release/release/v1.15.0/bin/linux/amd64/kubectl &&
	chmod +x ./kubectl &&
	mv ./kubectl /usr/local/bin/kubectl

# Install bizflyctl
BIZFLYCTL_VERSION="0.2.17"
curl -LfO https://github.com/bizflycloud/bizflyctl/releases/download/v${BIZFLYCTL_VERSION}/bizflyctl_Linux_x86_64.tar.gz &&
	tar xf bizflyctl_Linux_x86_64.tar.gz &&
	mv bizfly /usr/local/bin
rm -rf bizflyctl_Linux_x86_64.tar.gz
