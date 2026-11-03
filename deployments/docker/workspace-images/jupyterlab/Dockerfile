# Jenderal Cloud OS workspace image: jupyterlab
FROM python:3.13-bookworm
ARG DEBIAN_FRONTEND=noninteractive
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends bash ca-certificates curl git less nano vim-tiny procps unzip zip locales build-essential tmux \
 && rm -rf /var/lib/apt/lists/*
# Replace any pre-existing uid/gid 1000 account with the Jenderal Cloud OS workspace user.
RUN existing=$(getent passwd 1000 | cut -d: -f1); if [ -n "$existing" ]; then userdel -r "$existing" 2>/dev/null || userdel "$existing"; fi; \
    existing_g=$(getent group 1000 | cut -d: -f1); if [ -n "$existing_g" ]; then groupdel "$existing_g" 2>/dev/null || true; fi; \
    groupadd -g 1000 itk && useradd -m -u 1000 -g 1000 -s /bin/bash -d /home/itk itk
COPY common/itk-entrypoint /usr/local/bin/itk-entrypoint
COPY common/bashrc-snippet /tmp/bashrc-snippet
RUN chmod 0755 /usr/local/bin/itk-entrypoint && cat /tmp/bashrc-snippet >> /etc/skel/.bashrc && rm /tmp/bashrc-snippet
RUN pip install --no-cache-dir jupyterlab numpy pandas matplotlib
COPY jupyterlab/start-jupyter /usr/local/bin/start-jupyter
RUN chmod 0755 /usr/local/bin/start-jupyter
ENV LANG=C.UTF-8 HOME=/home/itk
USER 1000:1000
WORKDIR /home/itk
ENTRYPOINT ["/usr/local/bin/itk-entrypoint"]
CMD ["/usr/local/bin/start-jupyter"]
