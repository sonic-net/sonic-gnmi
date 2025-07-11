FROM sonicdev-microsoft.azurecr.io/debian:bookworm

# Create non-root user for security
RUN adduser --system --group opsd

# Set working directory
WORKDIR /usr/local/bin

# Copy the built binary
COPY bin/opsd-server /usr/local/bin/opsd-server

# Create directory for any config files
RUN mkdir -p /etc/opsd && chown -R opsd:opsd /etc/opsd

# Set proper permissions
RUN chmod +x /usr/local/bin/opsd-server

# Copy and set up the entrypoint script
COPY docker/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Copy any configuration files if needed
# COPY config/opsd.conf /etc/opsd/

# NOTE: Running as root is required for nsenter to access host namespaces
# and execute sonic-installer commands. The container is already isolated
# and runs with specific privileges only when needed.
# USER opsd

# Environment variables for configuration (can be overridden at runtime)
ENV OPSD_ADDR=":50051"
ENV OPSD_SHUTDOWN_TIMEOUT="10s"

# Note: Port exposure depends on MOPD_ADDR configuration
# Use -p flag in docker run to publish the actual port being used
# Example: docker run -p 8080:8080 -e MOPD_ADDR=":8080" <image>

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
