---
title: Linux Command Cheat Sheet
category: ""
tags:
  - starter
  - linux
favorite: false
archived: false
importRef: starter:notes:Linux Command Cheat Sheet
links: []
extras: {}
kind: cheatsheet
id: 01M80T620DB2YQM00H5QP7Z414
createdAt: 2026-11-22T05:03:11.645Z
updatedAt: 2026-11-22T05:03:11.645Z
---
# Linux Command Cheat Sheet

A one-page reminder. Each command has a full entry in the Commands library.

## Moving around

| Task | Command |
| --- | --- |
| Where am I? | `pwd` |
| List with details, including hidden | `ls -la` |
| Go home / back / up | `cd ~` · `cd -` · `cd ..` |
| Find files by name | `find . -name "*.conf"` |

## Looking at files

| Task | Command |
| --- | --- |
| Page through a file | `less file` (q to quit) |
| First / last 20 lines | `head -n 20 file` · `tail -n 20 file` |
| Watch a log grow | `tail -f file` |
| What kind of file is it? | `file mystery` |

## Searching and shaping text

| Task | Command |
| --- | --- |
| Lines containing a word | `grep -rn "word" dir/` |
| Count lines | `wc -l file` |
| Most frequent values | `sort file \| uniq -c \| sort -nr \| head` |
| Second column of a CSV | `cut -d, -f2 data.csv` |

## Permissions and users

| Task | Command |
| --- | --- |
| Who am I? | `id` |
| Make a script executable | `chmod +x script.sh` |
| Private key permissions | `chmod 600 key` |
| What can I run as root? | `sudo -l` |

> [!WARNING]
> `rm` has no undo. Preview with `ls` first, and prefer `rm -i` while learning.
