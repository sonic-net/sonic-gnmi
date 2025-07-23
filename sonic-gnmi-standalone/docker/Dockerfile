FROM sonicdev-microsoft.azurecr.io/debian:bookworm

# Set working directory
WORKDIR /app

# Copy the built binary
COPY bin/sonic-gnmi-standalone /usr/local/bin/sonic-gnmi-standalone

# Create directory for any config files
RUN mkdir -p /etc/gnmi

# Set proper permissions
RUN chmod +x /usr/local/bin/sonic-gnmi-standalone

# Copy and set up the entrypoint script
COPY docker/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Copy any configuration files if needed
# COPY config/gnmi.conf /etc/gnmi/

# Run as root to allow file system access when using --rootfs
# USER root (explicit but not needed since root is default)

# Environment variables for configuration (can be overridden at runtime)
ENV GNMI_ADDR=":50051"
ENV GNMI_SHUTDOWN_TIMEOUT="10s"

# Note: Port exposure depends on MOPD_ADDR configuration
# Use -p flag in docker run to publish the actual port being used
# Example: docker run -p 8080:8080 -e MOPD_ADDR=":8080" <image>

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
