# Jenderal Cloud OS workspace image: desktop-browser
# Firefox ESR on a virtual X display, streamed to the user's browser window
# with noVNC. The session starts on demand (itk-app-gate, port 6080) and stops
# after an idle period.
FROM debian:bookworm-slim
ARG DEBIAN_FRONTEND=noninteractive
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates bash procps locales tzdata \
      xvfb x11vnc matchbox-window-manager novnc python3-websockify \
      firefox-esr fonts-dejavu-core fonts-noto-core fonts-noto-color-emoji fonts-liberation2 \
      xdg-utils dbus-x11 \
 && rm -rf /var/lib/apt/lists/*
# Replace any pre-existing uid/gid 1000 account with the Jenderal Cloud OS workspace user.
RUN existing=$(getent passwd 1000 | cut -d: -f1); if [ -n "$existing" ]; then userdel -r "$existing" 2>/dev/null || userdel "$existing"; fi; \
    existing_g=$(getent group 1000 | cut -d: -f1); if [ -n "$existing_g" ]; then groupdel "$existing_g" 2>/dev/null || true; fi; \
    groupadd -g 1000 jenderal && useradd -m -u 1000 -g 1000 -s /bin/bash -d /home/jenderal jenderal
COPY common/jenderal-entrypoint /usr/local/bin/jenderal-entrypoint
COPY common/bashrc-snippet /tmp/bashrc-snippet
COPY desktop-browser/start-desktop-session /usr/local/bin/start-desktop-session
COPY desktop-browser/jenderal-firefox.js /usr/lib/firefox-esr/defaults/pref/jenderal-firefox.js
COPY desktop-browser/policies.json /usr/lib/firefox-esr/distribution/policies.json
RUN chmod 0755 /usr/local/bin/jenderal-entrypoint /usr/local/bin/start-desktop-session \
 && cat /tmp/bashrc-snippet >> /etc/skel/.bashrc && rm /tmp/bashrc-snippet

ENV LANG=C.UTF-8 HOME=/home/jenderal
USER 1000:1000
WORKDIR /home/jenderal
ENTRYPOINT ["/usr/local/bin/jenderal-entrypoint"]
CMD ["sleep", "infinity"]
