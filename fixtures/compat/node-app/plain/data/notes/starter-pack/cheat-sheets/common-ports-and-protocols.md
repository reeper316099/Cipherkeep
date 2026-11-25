---
title: Common Ports and Protocols
category: ""
tags:
  - starter
  - networking
favorite: false
archived: false
importRef: starter:notes:Common Ports and Protocols
links: []
extras: {}
kind: cheatsheet
id: 01M88G0CXQWWT2T1ZHR7N3D4ZY
createdAt: 2026-11-25T04:39:15.925Z
updatedAt: 2026-11-25T04:39:15.925Z
---
# Common Ports and Protocols

Defaults only: any service can listen on any port.

| Port | Protocol | Service |
| --- | --- | --- |
| 20, 21 | TCP | FTP (data, control) |
| 22 | TCP | SSH, SCP, SFTP |
| 23 | TCP | Telnet (unencrypted) |
| 25 | TCP | SMTP (mail between servers) |
| 53 | UDP/TCP | DNS |
| 67, 68 | UDP | DHCP (server, client) |
| 80 | TCP | HTTP |
| 110 | TCP | POP3 |
| 123 | UDP | NTP |
| 143 | TCP | IMAP |
| 161 | UDP | SNMP |
| 389 | TCP/UDP | LDAP |
| 443 | TCP | HTTPS |
| 445 | TCP | SMB (Windows file sharing) |
| 587 | TCP | SMTP submission (mail clients) |
| 993 | TCP | IMAPS |
| 995 | TCP | POP3S |
| 1433 | TCP | Microsoft SQL Server |
| 3306 | TCP | MySQL / MariaDB |
| 3389 | TCP | RDP (Windows Remote Desktop) |
| 5432 | TCP | PostgreSQL |
| 5900 | TCP | VNC |
| 6379 | TCP | Redis |
| 8080 | TCP | Alternate HTTP (proxies, dev servers) |

## Checking a port

- Is something listening here? `ss -tulpn` (Linux)
- Is that port reachable? `nc -zv host 443`
- What is running on it? `nmap -sV -p 443 host` (only against systems you may test)
