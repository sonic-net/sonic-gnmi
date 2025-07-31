#!/bin/bash
#
# Build and deploy sonic-gnmi-standalone as Docker container to remote SONiC device
#
# This script is for TESTING ONLY - not for production use.
# It builds a Docker image locally and deploys it to a remote SONiC device via SSH.
# The container runs with --privileged and mounts the entire host filesystem.
#
# Examples:
#   ./build_run_docker_testonly.sh -t admin@vlab-01              # Deploy to device
#   ./build_run_docker_testonly.sh -t admin@vlab-01 -a :8080     # Custom port
#   ./build_run_docker_testonly.sh -t admin@vlab-01 --enable-tls # Enable TLS
#   ./build_run_docker_testonly.sh -t admin@vlab-01 -i v1.0.0    # Custom image tag
#
# After deployment:
#   - Container name: gnmi-standalone-testonly
#   - View logs: ssh <target> docker logs gnmi-standalone-testonly
#   - Stop: ssh <target> docker rm -f gnmi-standalone-testonly

set -e

# Parse command line arguments
TARGET=""
IMAGE_TAG="latest"
SERVER_ADDR=":50055"
NO_TLS="true"

usage() {
  echo "Usage: $0 -t <target> [-i <image_tag>] [-a <address>] [--enable-tls]"
  echo "  -t <target>    Target SONiC device (e.g., admin@vlab-01)"
  echo "  -i <image_tag> Docker image tag (default: latest)"
  echo "  -a <address>   Server address to listen on (default: :50055)"
  echo "  --enable-tls   Enable TLS for secure connections (default: disabled)"
  exit 1
}

# Parse arguments (including long options)
while [[ $# -gt 0 ]]; do
  case $1 in
    -t)
      TARGET="$2"
      shift 2
      ;;
    -i)
      IMAGE_TAG="$2"
      shift 2
      ;;
    -a)
      SERVER_ADDR="$2"
      shift 2
      ;;
    --enable-tls)
      NO_TLS="false"
      shift
      ;;
    -h|--help)
      usage
      ;;
    *)
      echo "Unknown option: $1"
      usage
      ;;
  esac
done

if [ -z "$TARGET" ]; then
  echo "ERROR: Target SONiC device is required"
  usage
fi

# Directory where this script is located
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
ROOT_DIR="$( cd "$SCRIPT_DIR/.." && pwd )"

# Build the Docker image using make
cd "$ROOT_DIR"
echo "Building gnmi Docker image..."
make docker-build DOCKER_TAG=$IMAGE_TAG

# Check if image was built successfully
IMAGE_NAME="gnmi-standalone-test:$IMAGE_TAG"
if [[ "$(docker images -q $IMAGE_NAME 2> /dev/null)" == "" ]]; then
  echo "Failed to build $IMAGE_NAME image"
  exit 1
fi

echo "Docker image built successfully: $IMAGE_NAME"

# Deploy to the SONiC device
echo ""
echo "Deploying to $TARGET..."

# Save the Docker image to a temporary file
TEMP_IMAGE_FILE="/tmp/gnmi-$IMAGE_TAG.tar"
echo "Saving Docker image to $TEMP_IMAGE_FILE..."
docker save $IMAGE_NAME -o $TEMP_IMAGE_FILE

# Transfer the image to the SONiC device
echo "Transferring image to $TARGET..."
scp $TEMP_IMAGE_FILE $TARGET:/tmp/

# Load and run the container on the SONiC device
echo "Loading and starting container on $TARGET..."
# Build the container arguments (without binary name since entrypoint handles that)
CONTAINER_ARGS="--addr='$SERVER_ADDR' --rootfs=/host"
if [ "$NO_TLS" = "true" ]; then
  CONTAINER_ARGS="$CONTAINER_ARGS --no-tls"
fi

echo "DEBUG: Container arguments: $CONTAINER_ARGS"
echo "DEBUG: Full docker run command will be:"
echo "docker run -d --name gnmi-standalone-testonly --network host --privileged -v /:/host:rw $IMAGE_NAME $CONTAINER_ARGS"

ssh $TARGET "docker load -i /tmp/gnmi-$IMAGE_TAG.tar && \
              docker rm -f gnmi-standalone-testonly 2>/dev/null || true && \
              docker run -d --name gnmi-standalone-testonly --network host --privileged -v /:/host:rw $IMAGE_NAME $CONTAINER_ARGS"

# Clean up the temporary file
rm $TEMP_IMAGE_FILE

echo ""
echo "Deployment completed successfully!"
echo "Container 'gnmi-standalone-testonly' is now running on $TARGET"
echo "Listening on address: $SERVER_ADDR"
echo "TLS enabled: $([ "$NO_TLS" = "true" ] && echo "No" || echo "Yes")"
