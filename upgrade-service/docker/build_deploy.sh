#!/bin/bash
# Script to build and deploy the opsd container to a SONiC device
# Supports configurable server address for flexible deployment

set -e

# Parse command line arguments
TARGET=""
IMAGE_TAG="latest"
OPSD_ADDR=":50051"
DISABLE_TLS="true"

usage() {
  echo "Usage: $0 -t <target> [-i <image_tag>] [-a <address>] [--enable-tls]"
  echo "  -t <target>    Target SONiC device (e.g., admin@vlab-01)"
  echo "  -i <image_tag> Docker image tag (default: latest)"
  echo "  -a <address>   Server address to listen on (default: :50051)"
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
      OPSD_ADDR="$2"
      shift 2
      ;;
    --enable-tls)
      DISABLE_TLS="false"
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
echo "Building opsd Docker image..."
make docker-build DOCKER_TAG=$IMAGE_TAG

# Check if image was built successfully
IMAGE_NAME="opsd:$IMAGE_TAG"
if [[ "$(docker images -q $IMAGE_NAME 2> /dev/null)" == "" ]]; then
  echo "Failed to build $IMAGE_NAME image"
  exit 1
fi

echo "Docker image built successfully: $IMAGE_NAME"

# Deploy to the SONiC device
echo ""
echo "Deploying to $TARGET..."

# Save the Docker image to a temporary file
TEMP_IMAGE_FILE="/tmp/opsd-$IMAGE_TAG.tar"
echo "Saving Docker image to $TEMP_IMAGE_FILE..."
docker save $IMAGE_NAME -o $TEMP_IMAGE_FILE

# Transfer the image to the SONiC device
echo "Transferring image to $TARGET..."
scp $TEMP_IMAGE_FILE $TARGET:/tmp/

# Load and run the container on the SONiC device
echo "Loading and starting container on $TARGET..."
ssh $TARGET "docker load -i /tmp/opsd-$IMAGE_TAG.tar && \
              docker rm -f opsd 2>/dev/null || true && \
              docker run -d --name opsd --network host --privileged -v /:/host:rw -e OPSD_ADDR='$OPSD_ADDR' -e DISABLE_TLS='$DISABLE_TLS' $IMAGE_NAME -rootfs=/host"

# Clean up the temporary file
rm $TEMP_IMAGE_FILE

echo ""
echo "Deployment completed successfully!"
echo "Container 'opsd' is now running on $TARGET"
echo "Listening on address: $OPSD_ADDR"
echo "TLS enabled: $([ "$DISABLE_TLS" = "true" ] && echo "No" || echo "Yes")"
