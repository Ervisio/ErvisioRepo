# Decisions (date · decision · why)
2026-11-04 · Release v1.0.0 signed with ed25519 key kept offline (~/.ervisio-release/release.pem) · install.sh verifies SHA256SUMS.sig; CI disabled so maintainer builds locally.
2026-11-04 · Re-cut v1.0.2 instead of retagging v1.0.1 · tag predated the Dockerfile .npmrc fix; never move a pushed tag.
2026-11-04 · Plugin category "Web servers" in Ervisio/plugins · registry has no "Development"; plugin publishes through Caddy like apphost.
2026-11-04 · Missing actions storage policy disables cache/artifacts with a warning instead of failing startup · v1.0.2 crash-looped on generated configs; shipped configs now include [storage.policy.actions].
2026-11-04 · Plugin version always equals the server release tag; compose ERVISIO_VERSION default bumped per release · catalog requires version == latest tag; compose pulls the matching image.
2026-11-04 · Plugin modules never call React APIs at import time (createContext etc. created lazily) · the SDK react shim throws until activate() runs setReact; 1.0.4 shipped broken, 1.0.5 fixed it.
2026-11-04 · Catalog auto-merge only for releases whose manifest permissions are unchanged · a release adding commands/http rules needs a manual merge of the Ervisio/plugins sync PR.
