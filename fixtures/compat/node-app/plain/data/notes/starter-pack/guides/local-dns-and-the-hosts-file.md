---
title: Local DNS and the hosts file
category: ""
tags:
  - starter
  - dns
favorite: false
archived: false
importRef: starter:notes:Local DNS and the hosts file
links: []
extras: {}
kind: guide
id: 01M88G0CXQEQ55F0P2YCHTJZZA
createdAt: 2026-11-25T04:39:15.932Z
updatedAt: 2026-11-25T04:39:15.932Z
---
# Local DNS and the hosts file

The hosts file maps names to addresses on one machine and is consulted before DNS. Handy for lab setups (`web.lab.test` pointing at a VM) and for testing.

## Where it lives

| System | Path |
| --- | --- |
| Linux, macOS | `/etc/hosts` |
| Windows | `C:\Windows\System32\drivers\etc\hosts` |

Editing needs administrator rights (`sudo` on Linux/macOS, an elevated editor on Windows).

## Add an entry

```text
192.0.2.10   web.lab.test
```

One address, then one or more names, separated by spaces.

## Flush the DNS cache after changes

| System | Command |
| --- | --- |
| macOS | `sudo dscacheutil -flushcache; sudo killall -HUP mDNSResponder` |
| Windows | `ipconfig /flushdns` |
| Linux (systemd-resolved) | `resolvectl flush-caches` |

## Check it

```bash
ping -c 1 web.lab.test
dig +short web.lab.test      # asks DNS, so it will NOT show hosts-file entries
```

> [!NOTE]
> `dig` talks to DNS servers directly and ignores the hosts file; `ping` and browsers use the system resolver, which reads it.

Related: [[DNS (Domain Name System)]], [[cmd:dig]].
