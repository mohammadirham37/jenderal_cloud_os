# Jenderal Cloud OS workspace image: node-dev
FROM node:24-bookworm
ARG DEBIAN_FRONTEND=noninteractive
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash ca-certificates curl git less nano vim-tiny procps unzip zip locales build-essential python3 tmux \
 && rm -rf /var/lib/apt/lists/*
# Replace any pre-existing uid/gid 1000 account with the Jenderal Cloud OS workspace user.
RUN existing=$(getent passwd 1000 | cut -d: -f1); if [ -n "$existing" ]; then userdel -r "$existing" 2>/dev/null || userdel "$existing"; fi; \
    existing_g=$(getent group 1000 | cut -d: -f1); if [ -n "$existing_g" ]; then groupdel "$existing_g" 2>/dev/null || true; fi; \
    groupadd -g 1000 jenderal && useradd -m -u 1000 -g 1000 -s /bin/bash -d /home/jenderal jenderal
COPY common/jenderal-entrypoint /usr/local/bin/jenderal-entrypoint
COPY common/bashrc-snippet /tmp/bashrc-snippet
RUN chmod 0755 /usr/local/bin/jenderal-entrypoint && cat /tmp/bashrc-snippet >> /etc/skel/.bashrc && rm /tmp/bashrc-snippet
RUN corepack enable && npm config set --global prefix /home/jenderal/.local
ENV LANG=C.UTF-8 HOME=/home/jenderal
USER 1000:1000
WORKDIR /home/jenderal
ENTRYPOINT ["/usr/local/bin/jenderal-entrypoint"]
CMD ["sleep", "infinity"]
