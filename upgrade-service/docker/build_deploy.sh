#!/bin/bash
# Script to build and deploy the mopd container to a SONiC device
# Supports configurable server address for flexible deployment

set -e

# Parse command line arguments
TARGET=""
IMAGE_TAG="latest"
MOPD_ADDR=":50051"

usage() {
  echo "Usage: $0 -t <target> [-i <image_tag>] [-a <address>]"
  echo "  -t <target>    Target SONiC device (e.g., admin@vlab-01)"
  echo "  -i <image_tag> Docker image tag (default: latest)"
  echo "  -a <address>   Server address to listen on (default: :50051)"
  exit 1
}

while getopts "t:i:a:h" opt; do
  case ${opt} in
    t)
      TARGET=$OPTARG
      ;;
    i)
      IMAGE_TAG=$OPTARG
      ;;
    a)
      MOPD_ADDR=$OPTARG
      ;;
    h|*)
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
echo "Building mopd Docker image..."
make docker-build DOCKER_TAG=$IMAGE_TAG

# Check if image was built successfully
IMAGE_NAME="docker-mopd:$IMAGE_TAG"
if [[ "$(docker images -q $IMAGE_NAME 2> /dev/null)" == "" ]]; then
  echo "Failed to build $IMAGE_NAME image"
  exit 1
fi

echo "Docker image built successfully: $IMAGE_NAME"

# Deploy to the SONiC device
echo ""
echo "Deploying to $TARGET..."

# Save the Docker image to a temporary file
TEMP_IMAGE_FILE="/tmp/docker-mopd-$IMAGE_TAG.tar"
echo "Saving Docker image to $TEMP_IMAGE_FILE..."
docker save $IMAGE_NAME -o $TEMP_IMAGE_FILE

# Transfer the image to the SONiC device
echo "Transferring image to $TARGET..."
scp $TEMP_IMAGE_FILE $TARGET:/tmp/

# Load and run the container on the SONiC device
echo "Loading and starting container on $TARGET..."
ssh $TARGET "docker load -i /tmp/docker-mopd-$IMAGE_TAG.tar && \
              docker rm -f mopd 2>/dev/null || true && \
              docker run -d --name mopd --network host -v /host:/host:rw -e MOPD_ADDR='$MOPD_ADDR' $IMAGE_NAME"

# Clean up the temporary file
rm $TEMP_IMAGE_FILE

echo ""
echo "Deployment completed successfully!"
echo "Container 'mopd' is now running on $TARGET"
echo "Listening on address: $MOPD_ADDR"
