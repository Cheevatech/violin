---
name: violin-security
description: Keep Violin releases free of credentials and unsafe configuration writes.
---

# Violin security

Never commit provider credentials, local configuration, model caches, or
private keys. Preview configuration changes, preserve backups, redact secrets,
and run secret scanning before release.

Laya MCP tools are read-only/advisory: they must not execute arbitrary shell,
modify files, spawn workers during route/risk review, or return API keys,
tokens, raw secret-bearing environment values, or private model paths. Model
updates must continue to use the verified manifest/checksum flow. Skill
installation must remain previewable, backup-protected, and must not overwrite
unmanaged files.
