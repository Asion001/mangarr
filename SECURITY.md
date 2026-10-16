# Security policy

## Reporting a vulnerability

Please **don't open a public issue** for security problems. Report them
privately through GitHub:
[Report a vulnerability](https://github.com/Asion001/Mangarr/security/advisories/new).

Include what you found, how to reproduce it and which version you ran
(System → Status, or `mangarr version`). You'll get an answer as soon as
possible, and a fix and advisory will be published once it's patched.

## Supported versions

Security fixes go into `main` first and are released as a patch of the
newest minor version (for example `1.4.2` after `1.4.1`). Older minor and
major versions don't get fixes; upgrade to the newest release to get them.

| Version | Security fixes |
|---|---|
| Newest `1.x` release | Yes |
| Older `1.x` releases | No, upgrade |
| `0.x` and development builds before 1.0 | No |

## Scope

mangarr is meant to run on your own network or behind a reverse proxy.
Especially interesting are: authentication and API key bypasses, access to
another account's library or requests, path traversal in library or import
handling, SSRF through source, module or webhook URLs, and secrets leaking
into logs or diagnostics zips.
