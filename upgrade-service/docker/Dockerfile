FROM sonicdev-microsoft.azurecr.io/debian:bookworm

# Create non-root user for security
RUN groupadd -r mopd && useradd -r -g mopd -s /bin/false mopd

# Set working directory
WORKDIR /usr/local/bin

# Copy the built binary
COPY bin/server /usr/local/bin/mopd

# Create directory for any config files
RUN mkdir -p /etc/mopd && chown -R mopd:mopd /etc/mopd

# Set proper permissions
RUN chmod +x /usr/local/bin/mopd

# Copy and set up the entrypoint script
COPY docker/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Copy any configuration files if needed
# COPY config/mopd.conf /etc/mopd/

# Switch to non-root user
USER mopd

# Environment variables for configuration (can be overridden at runtime)
ENV MOPD_ADDR=":50051"
ENV MOPD_SHUTDOWN_TIMEOUT="10s"

# Note: Port exposure depends on MOPD_ADDR configuration
# Use -p flag in docker run to publish the actual port being used
# Example: docker run -p 8080:8080 -e MOPD_ADDR=":8080" <image>

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
