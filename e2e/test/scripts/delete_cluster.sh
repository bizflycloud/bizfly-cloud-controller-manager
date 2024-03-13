#!/bin/bash

# Default values
CLUSTER_NAME=""

usage() {
	echo "Usage: $0 [-n|--name <name>]"
	exit 1
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	-n | --name)
		CLUSTER_NAME="$2"
		shift 2
		;;
	*)
		echo "Invalid option: $1" >&2
		usage
		;;
	esac
done

if [[ -z $CLUSTER_NAME ]]; then
	echo "Missing required options"
	usage
fi

# Set timeout duration in seconds (10 minutes)
timeout_duration=$((10 * 60))

# Get the start time
start_time=$(date +%s)

CLUSTER_UID=$(bizfly kubernetes list | grep $CLUSTER_NAME | awk '{print $1}')
echo $CLUSTER_UID

if [ -z "$CLUSTER_UID" ]; then
	echo "CLUSTER_UID is empty. Exiting."
	exit 1
fi

bizfly kubernetes get $CLUSTER_UID | grep $CLUSTER_UID | awk '{print $6}'

if [ "$CLUSTER_UID" == "" ]; then
	echo "Cluster not found. Exiting..."
	exit 1
fi

bizfly kubernetes delete $CLUSTER_UID

# Loop until STATUS changes to "DESTROYED" or timeout occurs
while true; do
	# Execute the command and store the output in STATUS variable
	STATUS=$(bizfly kubernetes get $CLUSTER_UID | grep $CLUSTER_UID | awk '{print $6}')

	# Print the current status
	echo "Current status: $STATUS"

	# Check if the status is "DESTROYED"
	if [ "$STATUS" != "DESTROYING" ]; then
		echo "Status is DESTROYED. Exiting loop."
		break
	fi

	# Get the current time
	current_time=$(date +%s)

	# Calculate the elapsed time
	elapsed_time=$((current_time - start_time))

	# Check if the elapsed time has exceeded the timeout duration
	if [ $elapsed_time -ge $timeout_duration ]; then
		echo "Timeout exceeded. Exiting loop."
		break
	fi

	# If status is not yet "PROVISIONED" and timeout has not occurred, wait for some time before checking again
	sleep 5
done
