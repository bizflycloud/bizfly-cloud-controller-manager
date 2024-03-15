#!/bin/bash

# Function to generate a random string of 5 characters
generate_random_string() {
	openssl rand -base64 5 | tr -dc 'a-zA-Z0-9' | head -c 5
}

# Default values
CLUSTER_NAME="test-ccm-$(generate_random_string)"
VERSION=""
VPC=""
APP_CRED_ID=""
APP_CRED_SECRET=""
EMAIL=""
PASSWORD=""
IMAGE=""
TOKEN=""

# Function to display usage information
usage() {
	echo "Usage: $0 [-v|--version <version>] [-P|--vcp <vpc>] [-u|--app-cred-id <application credential id>] [-p|--app-cred-secret <application credential secret>] [--email <email>] [--password <password>] [--image <ccm-new-image-hash>] [--token <secret-token>]"
	exit 1
}

# Parse command line options
while [[ $# -gt 0 ]]; do
	case "$1" in
	-v | --version)
		VERSION="$2"
		shift 2
		;;
	-P | --vpn)
		VPC="$2"
		shift 2
		;;
	-u | --app-cred-id)
		APP_CRED_ID="$2"
		shift 2
		;;
	-p | --app-cred-secret)
		APP_CRED_SECRET="$2"
		shift 2
		;;
	--email)
		EMAIL="$2"
		shift 2
		;;
	--password)
		PASSWORD="$2"
		shift 2
		;;
	--image)
		IMAGE="$2"
		shift 2
		;;
	--token)
		TOKEN="$2"
		shift 2
		;;
	*)
		echo "Invalid option: $1" >&2
		usage
		;;
	esac
done

# Check if all options are provided
if [[ -z $VERSION || -z $VPC || -z $APP_CRED_ID || -z $APP_CRED_SECRET || -z $EMAIL || -z $PASSWORD || -z $IMAGE || -z $TOKEN ]]; then
	echo "Missing required options"
	usage
fi

echo "==> installing dependencies..."
SCRIPTS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
"${SCRIPTS_DIR}/test/scripts/install_deps.sh"
export BIZFLY_CLOUD_EMAIL=$EMAIL
export BIZFLY_CLOUD_PASSWORD=$PASSWORD

echo "==> running E2E tests..."
echo "use image echo $IMAGE"
CLUSTER_UID=$(bizfly kubernetes list | grep $CLUSTER_NAME | awk '{print $1}')
echo $CLUSTER_UID

if [ "$CLUSTER_UID" != "" ]; then
	## Use the existing cluster
	echo "Using existing cluster $CLUSTER_UID..."
	bizfly kubernetes kubeconfig get $CLUSTER_UID
	export KUBECONFIG=$CLUSTER_UID.kubeconfig
	export CLUSTER_UID=$CLUSTER_UID
else
	# Set timeout duration in seconds (10 minutes)
	timeout_duration=$((10 * 60))

	# Get the start time
	start_time=$(date +%s)

	# Provision a new cluster
	CLUSTER_UID=$(
		bizfly kubernetes create --name $CLUSTER_NAME --version $VERSION --vpc-network-id $VPC \
			--worker-pool "name=worker-1;flavor=nix.2c_2g;profile_type=premium;volume_type=PREMIUM-HDD1;volume_size=40;availability_zone=HN1;desired_size=1;min_size=1;max_size=1;labels=test-ccm=node01" \
			--worker-pool "name=worker-2;flavor=nix.2c_2g;profile_type=premium;volume_type=PREMIUM-HDD1;volume_size=40;availability_zone=HN1;desired_size=1;min_size=1;max_size=1;labels=test-ccm=node02" |
			grep $CLUSTER_NAME | awk '{print $1}'
	)
	echo "Creating cluster $CLUSTER_UID..."

	# Loop until STATUS changes to "PROVISIONED" or timeout occurs
	while true; do
		# Execute the command and store the output in STATUS variable
		STATUS=$(bizfly kubernetes get $CLUSTER_UID | grep $CLUSTER_UID | awk '{print $6}')

		# Print the current status
		echo "Current status: $STATUS"

		# Check if the status is "PROVISIONED"
		if [ "$STATUS" = "PROVISIONED" ]; then
			echo "Status is PROVISIONED. Exiting loop."
			break
		fi

		# Get the current time
		current_time=$(date +%s)

		# Calculate the elapsed time
		elapsed_time=$((current_time - start_time))

		# Check if the elapsed time has exceeded the timeout duration
		if [ $elapsed_time -ge $timeout_duration ]; then
			echo "Timeout exceeded. Exiting loop."
			exit 1
		fi

		# If status is not yet "PROVISIONED" and timeout has not occurred, wait for some time before checking again
		sleep 5
	done

	bizfly kubernetes kubeconfig get $CLUSTER_UID
	export CLUSTER_UID=$CLUSTER_UID
	export KUBECONFIG="${CLUSTER_UID}.kubeconfig"

	# Patch new ccm image
	curl --location "https://manage.bizflycloud.vn/api/kubernetes-engine/ccm/${CLUSTER_UID}" \
		--header "Authorization: ${TOKEN}" \
		--header 'Content-Type: application/json' \
		--data "{\"ccm_image\": \"$IMAGE\"}"

	# Wait for workers to show up
	check_nodes_ready() {
		local ready_nodes=$(kubectl get nodes --no-headers | grep -c 'Ready')
		if [ $ready_nodes -ge 2 ]; then
			return 0
		else
			return 1
		fi
	}

	# Main loop to continuously check the status of nodes
	while true; do
		if check_nodes_ready; then
			echo "Minimum 2 nodes are ready."
			break
		else
			echo "Waiting for nodes to be ready..."
			sleep 5 # Adjust the sleep duration as needed
		fi
	done

fi

ginkgo -v --json-report=report.json -- \
	--bizfly-url="https://manage.bizflycloud.vn" \
	--auth-method="application_credential" \
	--app-cred-id="$APP_CRED_ID" \
	--use-existing=false \
	--app-cred-secret="$APP_CRED_SECRET" \
	--cluster-uid="$CLUSTER_UID"
