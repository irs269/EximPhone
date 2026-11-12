# Garage (S3-compatible object storage) + a self-initialising entrypoint.
# The upstream image has no shell, so we copy its static binary onto alpine.
FROM dxflrs/garage:v1.0.1 AS garage
FROM alpine:3.20
COPY --from=garage /garage /usr/local/bin/garage
COPY garage.toml /etc/garage.toml
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENV GARAGE_CONFIG_FILE=/etc/garage.toml
ENTRYPOINT ["/entrypoint.sh"]
