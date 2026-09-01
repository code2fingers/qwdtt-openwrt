ARG ARCH
FROM ghcr.io/openwrt/sdk:${ARCH}

# The published images carry setup.sh rather than the SDK itself, so the
# entrypoint fetches the tarball at container start. Removing the script is
# what stops that happening again once the SDK is in a layer: the entrypoint
# runs it whenever the file is present.
RUN bash /builder/setup.sh && rm -f /builder/setup.sh
