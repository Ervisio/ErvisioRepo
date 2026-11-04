# Runner images

`ervisio-runner` runs every job in a Docker container whose image is chosen by
the job's `runs-on` label. The mapping lives in `runner.labels` of
`/etc/ervisio-runner/config.toml`; `labels.example.toml` is a starting point.

## Defaults

| Label           | Image                                      |
|-----------------|--------------------------------------------|
| `ubuntu-latest` | `ghcr.io/catthehacker/ubuntu:act-24.04`    |
| `ubuntu-24.04`  | `ghcr.io/catthehacker/ubuntu:act-24.04`    |
| `ubuntu-22.04`  | `ghcr.io/catthehacker/ubuntu:act-22.04`    |

The `act-*` images carry Node.js, git and the common CLI tools that JavaScript
actions expect. The `full-*` variants are close to GitHub's hosted runners but
weigh about 20 GB; pre-pull them (`docker pull`) on the runner host.

## Custom images

Any image works if it has `bash`/`sh`, `git` and, for JavaScript actions,
`node` on `PATH`. Build it `FROM ghcr.io/catthehacker/ubuntu:act-24.04` to keep
the toolchain, push it to a registry the runner can reach, then map a label:

```toml
[runner]
labels = ["ubuntu-latest=docker://registry.example.com/ci/ubuntu:24.04"]
```

Set `container.force_pull = true` to refresh mutable tags before every job.

## Isolation

Job containers are never privileged, keep Docker's default seccomp profile and
run with `--security-opt=no-new-privileges` and the cgroup limits of
`[container]` (`cpus`, `memory`, `pids_limit`). Only the volumes listed in
`container.valid_volumes` can be mounted. `name=host` labels run jobs directly
on the runner machine and are refused unless `[host] enabled = true`.

---

# Immagini dei runner

`ervisio-runner` esegue ogni job in un container Docker la cui immagine dipende
dall'etichetta `runs-on` del job. La mappa è in `runner.labels` di
`/etc/ervisio-runner/config.toml`; `labels.example.toml` è un punto di partenza.
Le immagini predefinite sono quelle della tabella sopra (`catthehacker/ubuntu`,
varianti `act-*`). Un'immagine personalizzata deve avere `sh`, `git` e `node`
nel `PATH`. I container non sono mai privilegiati, mantengono il profilo seccomp
predefinito di Docker, usano `no-new-privileges` e i limiti cgroup di
`[container]`; le etichette `name=host` richiedono `[host] enabled = true`.
