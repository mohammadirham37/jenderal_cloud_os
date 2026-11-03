# Workspace images

These Dockerfiles build the images used by the built-in workspace templates.
They are **workload images for user workspaces**; Jenderal Cloud OS itself is installed
natively on the host (see docs/installation.md) and does not run in Docker.

All images:

- run as the unprivileged user `jenderal` (uid/gid 1000), never root;
- contain no SSH server and no sudo;
- use `/home/jenderal` as the home directory, which Jenderal Cloud OS bind-mounts from the
  workspace volume (quota-limited);
- start with `jenderal-entrypoint`, which seeds shell dotfiles into an empty home.

Build all images on the server with:

```bash
sudo installer/build-workspace-images.sh          # all images
sudo installer/build-workspace-images.sh ubuntu-dev python-dev
```

Image tags (`itk-cloud/<name>:1.0`) must match the `container_images` catalog.
After rebuilding, administrators can press **Check / pull** on the image in
*Administration → Images* to refresh its status.
