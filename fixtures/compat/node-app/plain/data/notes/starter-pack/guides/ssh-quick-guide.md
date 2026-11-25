---
title: SSH Quick Guide
category: ""
tags:
  - starter
  - ssh
favorite: false
archived: false
importRef: starter:notes:SSH Quick Guide
links: []
extras: {}
kind: guide
id: 01M88G0CXQSMCGZZENAY11X91Y
createdAt: 2026-11-25T04:39:15.928Z
updatedAt: 2026-11-25T04:39:15.928Z
---
# SSH Quick Guide

## Make a key pair

```bash
ssh-keygen -t ed25519 -C "lab laptop"
```

Accept the default location and set a passphrase. This creates `~/.ssh/id_ed25519` (private, never share) and `~/.ssh/id_ed25519.pub` (public).

## Install the public key on a server

```bash
ssh-copy-id student@192.0.2.10
```

## Permissions SSH insists on

```bash
chmod 700 ~/.ssh
chmod 600 ~/.ssh/id_ed25519
```

## A tidy `~/.ssh/config`

```text
Host lab
  HostName 192.0.2.10
  User student
  Port 22
  IdentityFile ~/.ssh/id_ed25519
```

Then `ssh lab` is enough.

## Tunnels

| Goal | Command |
| --- | --- |
| Reach a remote web server locally | `ssh -L 8080:localhost:80 lab` then open http://localhost:8080 |
| SOCKS proxy through the host | `ssh -D 1080 -N lab` |
| Go through a jump host | `ssh -J jump@198.51.100.5 student@10.0.0.7` |

> [!TIP]
> On first connection compare the host key fingerprint with one you trust. A changed key later means a rebuilt server, or someone in the middle.

Related: [[cmd:ssh]], [[cmd:scp]], [[File Permissions]].
