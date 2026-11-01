# Cosmics — Tool Registry & Kali Image Strategy

**Version:** 1.0.0
**Status:** Normative
**Verified:** Docker 29.9.2, Linux 29.9.2 engine, x86_64, 2026-11-01

---

## 1. Verified environment facts

These were measured on the target machine, not assumed.

| Fact | Measurement | Consequence |
|---|---|---|
| Docker engine | `29.9.2`, `OSType=linux`, `x86_64` | Linux containers via WSL2 backend |
| `kalilinux/kali-rolling:latest` | **121 MB** | It is a **minimal base** |
| Tools present in that base | `nmap` ✗, `nikto` ✗, `sqlmap` ✗, `hydra` ✗, `nuclei` ✗, `ffuf` ✗, `subfinder` ✗, `httpx` ✗, `whatweb` ✗ | **Zero security tools ship in the base** |
| `apt-get` present | ✓ | Tools must be installed at image build time |
| `--network host` | Runs, but is a no-op relative to the Windows host on Docker Desktop | Cannot see host-network traffic; only container-to-internet egress |
| Docker socket | Not mounted into any Cosmics container | Containers cannot control Docker |

**This invalidates a common assumption:** there is no "just use the Kali image" path. Every tool must be baked in, and the image is therefore a first-class build artifact that must be versioned alongside the code.

---

## 2. Image strategy

### 2.1 Decision: one layered image, not one image per tool

| Option | Verdict |
|---|---|
| One fat Kali image with everything | ✅ **Chosen.** One pull, shared layers, `apt` cache reused, simplest lifecycle. |
| One image per tool | ❌ Rejected. 20+ pulls, 20+ base layers, version drift, and the operator waits on each. |
| Vendor images (`projectdiscovery/*`, `instrumentisto/nmap`) | ❌ Rejected as primary. Useful upstreams, but mixed bases and inconsistent entrypoints complicate argv templating. |

**Exception:** if a specific tool requires an incompatible runtime (e.g. a Windows-only binary, or a tool that must run on the host for fidelity), it gets its own image. None are in v1.

### 2.2 Layer design

Split into three tagged images so a tool addition does not rebuild the base:

```
cosmics/kali-base:<ver>     Debian/Kali + apt lists + common runtime deps + wordlists
cosmics/kali-tools:<ver>    FROM kali-base + the full tool set          ← default
cosmics/kali-exploit:<ver>  FROM kali-tools + metasploit/impacket      ← large, opt-in
```

The registry references image **tags**, and `tool_runs.image_digest` records the resolved digest at run time.

### 2.3 Dockerfile

```dockerfile
# docker/Dockerfile
# syntax=docker/dockerfile:1.7

ARG KALI_TAG=latest

FROM kalilinux/kali-rolling:${KALI_TAG} AS base

ENV DEBIAN_FRONTEND=noninteractive \
    LC_ALL=C.UTF-8 \
    LANG=C.UTF-8 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl wget git jq unzip xz-utils \
        python3 python3-pip python3-venv \
        dnsutils whois iputils-ping traceroute netcat-traditional \
        openssl libssl3 \
        procps less file \
    && rm -rf /var/lib/apt/lists/*

# ---------- recon / network ----------
RUN apt-get update && apt-get install -y --no-install-recommends \
        nmap masscan dnsx dnsrecon whois \
        whatweb wafw00f sslscan \
        nikto gobuster ffuf feroxbuster \
    && rm -rf /var/lib/apt/lists/*

# ---------- web / exploitation ----------
RUN apt-get update && apt-get install -y --no-install-recommends \
        sqlmap hydra wpscan nuclei \
        amass assetfinder \
    && rm -rf /var/lib/apt/lists/*

# ---------- projectdiscovery (release binaries, arch-aware) ----------
ARG PD_VERSION=latest
RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in \
      amd64) pdarch=amd64 ;; \
      arm64) pdarch=arm64 ;; \
      *) echo "unsupported arch: $arch" >&2; exit 1 ;; \
    esac; \
    for tool in subfinder httpx katana naabu tlsx; do \
      ver="$PD_VERSION"; \
      if [ "$ver" = "latest" ]; then \
        ver="$(curl -fsSL "https://api.github.com/repos/projectdiscovery/${tool}/releases/latest" | jq -r .tag_name | sed 's/^v//')"; \
      fi; \
      url="https://github.com/projectdiscovery/${tool}/releases/download/v${ver}/${tool}_${ver}_linux_${pdarch}.zip"; \
      curl -fsSL "$url" -o /tmp/${tool}.zip; \
      unzip -oq /tmp/${tool}.zip -d /usr/local/bin/; \
      rm -f /tmp/${tool}.zip; \
      chmod +x /usr/local/bin/${tool}; \
    done

# ---------- wordlists ----------
RUN mkdir -p /opt/wordlists && \
    curl -fsSL https://raw.githubusercontent.com/danielmiessler/SecLists/master/Discovery/Web-Content/common.txt \
      -o /opt/wordlists/common.txt && \
    curl -fsSL https://raw.githubusercontent.com/danielmiessler/SecLists/master/Discovery/DNS/subdomains-top1million-5000.txt \
      -o /opt/wordlists/subdomains-5000.txt

WORKDIR /work
ENTRYPOINT []
CMD ["/bin/bash"]
```

### 2.4 Exploit layer (opt-in)

```dockerfile
# docker/Dockerfile.exploit
FROM cosmics/kali-tools:latest

RUN apt-get update && apt-get install -y --no-install-recommends \
        metasploit-framework \
        python3-impacket impacket-scripts \
        crackmapexec smbclient ldap-utils \
        responder bettercap \
        john hashcat \
    && rm -rf /var/lib/apt/lists/*

# convenience: peass-ng transfer helpers
RUN mkdir -p /opt/peass && \
    curl -fsSL https://raw.githubusercontent.com/peass-ng/PEASS-ng/master/linPEAS/linpeas.sh \
      -o /opt/peass/linpeas.sh && chmod +x /opt/peass/linpeas.sh
```

**Build size warning:** the exploit layer is multi-GB. It is a separate tag so the default install path stays reasonable, and `cosmics tools pull --profile exploit` is explicit.

### 2.5 Build script

```powershell
# scripts/build-images.ps1
param(
  [string]$Tag = (Get-Date -Format 'yyyyMMdd'),
  [switch]$Exploit
)
$ErrorActionPreference = 'Stop'

docker build -f docker/Dockerfile -t "cosmics/kali-tools:$Tag" .
docker tag "cosmics/kali-tools:$Tag" "cosmics/kali-tools:latest"

if ($Exploit) {
  docker build -f docker/Dockerfile.exploit -t "cosmics/kali-exploit:$Tag" .
  docker tag "cosmics/kali-exploit:$Tag" "cosmics/kali-exploit:latest"
}

Write-Host "Built cosmics/kali-tools:$Tag"
docker images --format '{{.Repository}}:{{.Tag}} {{.Size}}' |
  Where-Object { $_ -like 'cosmics/*' }
```

### 2.6 Verification

`cosmics tools doctor` runs, per registry entry:

```
docker run --rm --entrypoint sh <image> -c 'command -v <binary> && <version_cmd>'
```

and records the observed version string. A mismatch between the registry's expected and observed version is a **warning**, not a failure — Kali rolls versions continuously, and pinning every tool version makes the image unbuildable within weeks.

---

## 3. Registry schema

```yaml
id: nmap
category: network
display_name: Nmap
image: cosmics/kali-tools:latest
binary: nmap
version_cmd: ["--version"]
danger_level: safe
requires_approval: false
capabilities: [NET_RAW]
network_mode: bridge
default_timeout_s: 900
max_output_bytes: 1048576
parser: nmap_xml

argument_schema:
  type: object
  additionalProperties: false
  required: [target]
  properties:
    target:
      type: string
      description: Host, IP, or CIDR range
      pattern: '^[A-Za-z0-9._:/\-\[\]]+$'
    ports:        {type: string, description: "1-1000 or 22,80,443"}
    top_ports:    {type: integer, minimum: 1, maximum: 65535}
    scan_type:    {type: string, enum: [connect, syn, udp], default: connect}
    service_detection: {type: boolean, default: true}
    scripts:      {type: array, items: {type: string}}
    extra_args:   {type: array, items: {type: string}}

command_template: >
  nmap {{if eq .scan_type "syn"}}-sS{{else if eq .scan_type "udp"}}-sU{{else}}-sT{{end}}
  {{if .top_ports}}--top-ports {{.top_ports}}{{end}}
  {{if .ports}}-p {{.ports}}{{end}}
  {{if .service_detection}}-sV -sC{{end}}
  {{range .scripts}}--script {{.}} {{end}}
  {{range .extra_args}}{{.}} {{end}}
  -oX - {{.target}}

fidelity_caveats:
  - "SYN scan (-sS) requires NET_RAW and is unreliable under Docker Desktop; connect scan (-sT) is the default."
```

### 3.1 Field semantics

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | Stable slug; primary key in `tool_defs` |
| `category` | yes | `recon`\|`osint`\|`web`\|`network`\|`exploit`\|`post`\|`report`; drives the stage allow-list |
| `image` | yes | Image reference; digest recorded per run |
| `binary` | yes | Entrypoint binary name; used by `doctor` |
| `version_cmd` | no | Argv appended to `binary` to print a version |
| `danger_level` | yes | `safe`\|`intrusive`\|`destructive`; drives the approval gate |
| `requires_approval` | yes | Forces the gate regardless of danger level |
| `capabilities` | no | Docker `--cap-add` values |
| `network_mode` | yes | `bridge` (default) \| `host` \| `none` |
| `default_timeout_s` | yes | Per-run timeout; overridable per engagement within a hard max |
| `max_output_bytes` | yes | Context truncation budget; full output stays in the artifact |
| `parser` | yes | `nmap_xml`\|`json`\|`jsonl`\|`lines`\|`regex_table`\|`none` |
| `argument_schema` | yes | JSON Schema; validated before any command is built |
| `command_template` | yes | Go `text/template` producing **argv tokens**, not a shell string |
| `fidelity_caveats` | no | Injected into the model-facing tool description |
| `shell_wrapper` | no | `true` only when a real shell is unavoidable; the wrapper is a fixed embedded script |

### 3.2 Argv construction (security-critical)

```
template render  →  shell-words split  →  argv[]  →  docker run ... <argv...>
```

- **Never** `sh -c "<rendered>"`.
- The renderer emits a single string which is split with a shell-words parser (respecting quotes) but **not** executed by a shell. No globbing, no substitution, no piping.
- `argument_schema` uses `additionalProperties: false`, so the model cannot inject unexpected flags.
- `pattern` constraints on host/IP fields reject injection-shaped input before it reaches the template.

This is why a model emitting `target: "example.com; rm -rf /"` is inert: it fails the schema `pattern`, and even if it passed, it would arrive as one argv token, not a command separator.

---

## 4. v1 Tool Catalog

### 4.1 Recon (M3)

| id | binary | category | danger | approval | parser | notes |
|---|---|---|---|---|---|---|
| `dns_lookup` | `dig` | recon | safe | no | `lines` | A/AAAA/MX/TXT/NS/CAA |
| `whois_lookup` | `whois` | recon | safe | no | `lines` | Registrant, dates |
| `subfinder` | `subfinder` | recon | safe | no | `jsonl` | Passive subdomains |
| `dnsx` | `dnsx` | recon | safe | no | `jsonl` | Resolution + wildcard detection |
| `httpx` | `httpx` | recon | safe | no | `jsonl` | Status, title, tech, TLS |
| `naabu` | `naabu` | recon | safe | no | `jsonl` | Fast port discovery |
| `nmap` | `nmap` | network | safe | no | `nmap_xml` | Service/version/scripts |
| `masscan` | `masscan` | network | intrusive | no | `lines` | Wide range; NET_RAW |
| `tlsx` | `tlsx` | recon | safe | no | `jsonl` | Cert SANs, expiry, ciphers |
| `whatweb` | `whatweb` | recon | safe | no | `json` | Tech fingerprint |
| `wafw00f` | `wafw00f` | recon | safe | no | `lines` | WAF detection |
| `sslscan` | `sslscan` | recon | safe | no | `lines` | Protocol/cipher weaknesses |

### 4.2 OSINT (M4)

| id | source | auth | notes |
|---|---|---|---|
| `crtsh` | crt.sh | none | Certificate transparency subdomains |
| `rdap` | public RDAP | none | Registration data (WHOIS replacement) |
| `wayback` | web.archive.org CDX | none | Historical URLs |
| `github_search` | GitHub API | optional `GITHUB_TOKEN` | Code/secret search |
| `shodan_host` | Shodan | `SHODAN_API_KEY` | No-ops cleanly when absent |
| `censys_search` | Censys | `CENSYS_API_ID/SECRET` | No-ops when absent |
| `virustotal_domain` | VirusTotal | `VT_API_KEY` | No-ops when absent |

**All OSINT tools are pure Go HTTP clients, not containers.** They send no traffic to the target, and running them in Docker would add latency for no isolation benefit. This is the one deliberate exception to D3, and it is safe precisely because they never touch the target.

### 4.3 Exploit / post (M5)

| id | binary | danger | approval | notes |
|---|---|---|---|---|
| `nuclei` | `nuclei` | intrusive | yes | Template-driven validation |
| `ffuf` | `ffuf` | intrusive | yes | Content/param discovery |
| `sqlmap` | `sqlmap` | destructive | **yes** | `--batch`, no dump by default |
| `nikto` | `nikto` | intrusive | yes | Web server checks |
| `hydra` | `hydra` | destructive | **yes** | Credential attacks; low rate default |
| `wpscan` | `wpscan` | intrusive | yes | WordPress |
| `gobuster` / `feroxbuster` | — | intrusive | yes | Directory brute force |
| `metasploit` | `msfconsole -r` | destructive | **yes** | Exploit layer only |
| `impacket_*` | — | destructive | **yes** | SMB/LDAP/WMI; exploit layer |
| `linpeas` | `sh /opt/peass/linpeas.sh` | intrusive | yes | Transferred, then run |

---

## 5. Execution Contract

```
validate schema → gate (approval) → render argv → docker run → stream → persist
```

### 5.1 `docker run` shape

```
docker run --rm -i
  --label cosmics.run=<run-id>
  --label cosmics.engagement=<engagement-id>
  --label cosmics.tool=<tool-id>
  --network <network_mode>
  --cap-add <cap>            (repeated; only the caps the registry declares)
  --memory 2g --memory-swap 2g
  --cpus 2
  --pids-limit 512
  --read-only
  --tmpfs /tmp:rw,noexec,nosuid,size=512m
  -v <host-workspace>:/work
  -w /work
  --user 0:0
  <image> <argv...>
```

**Why `--read-only`:** tools write their output to stdout (captured as an artifact) or to `/work` (the mounted workspace). A read-only rootfs prevents a tool from modifying the image layer and makes container state disposable. Tools that need to write elsewhere get an explicit `tmpfs`.

**Why `--user 0:0`:** many security tools drop privileges or need raw sockets and behave inconsistently as non-root. The container is ephemeral, read-only, capability-limited, and has no host mounts beyond the workspace — running as root inside it is not the same risk as running as root on the host.

### 5.2 Timeout and kill

```
ctx, cancel := context.WithTimeout(parent, spec.Timeout)
cmd := exec.CommandContext(ctx, "docker", args...)
```

On timeout, `CommandContext` kills the **CLI client**, which does **not** reliably kill the container. The executor therefore also runs:

```
docker kill --signal SIGKILL $(docker ps -q --filter label=cosmics.run=<run-id>)
```

and only then marks `status=timeout`. Verified required: without this, timed-out scans keep running and consuming CPU.

### 5.3 Streaming

```
stdout pipe ─┐
             ├─► line scanner ─► StreamSink{Stream: "stdout", Line: ...}
stderr pipe ─┘                └─► artifact file + incremental SHA-256
```

The sink is buffered (1024 lines). Beyond that, lines are counted and dropped, and a single event reports `dropped: N` — a flood of output must not block the container (which would deadlock the pipe) or the TUI.

### 5.4 Artifacts

```
~/.cosmics/workspace/<engagement>/
  runs/<run-id>/stdout.log
  runs/<run-id>/stderr.log
  artifacts/<sha256[0:16]>-<name>
```

- Written incrementally, hashed as written.
- On completion, the artifact row stores `byte_size`, `sha256`, `rel_path`.
- Identical content from two runs dedups (same `sha256`), which is common with repeated scans of an unchanged host.

### 5.5 Reaping

On startup and every 5 minutes:

```
docker ps -aq --filter label=cosmics.run --filter status=exited
docker ps -aq --filter label=cosmics.run --filter status=created
```

plus any labeled container whose `cosmics.run` id has no corresponding `tool_runs` row in `queued`/`running`. Removed containers are logged and emit an event.

---

## 6. Capability and fidelity policy

| Tool need | Flags | Docker Desktop reality | Cosmics behavior |
|---|---|---|---|
| TCP connect scan | none | Works fully | Default |
| SYN scan | `NET_RAW` | Unreliable; cannot see host namespace | Allowed, `fidelity: reduced` recorded, caveat injected into context |
| UDP scan | `NET_RAW` | Partially works | Allowed with caveat |
| ARP scan | `NET_RAW` + `host` | `host` is a no-op vs the Windows host | **Refused** with an explanation |
| Packet capture | `NET_RAW`,`NET_ADMIN` | Sees container traffic only, not host | Allowed but warns it cannot observe host traffic |
| Privileged tools | `--privileged` | Discouraged | Registry omits them from v1 |

**The invariant:** Cosmics never silently degrades. If fidelity is reduced, it is written to `tool_runs.parsed_json.fidelity = "reduced"` **and** injected into the model's context via `fidelity_caveats`, so the model does not treat a connect scan as equivalent evidence to a SYN scan.

---

## 7. Adding a tool

1. Add a YAML file to `internal/tools/registry/`.
2. If the binary is not in the image, add an `apt-get install` (or binary download) line to `docker/Dockerfile` and rebuild.
3. Implement or reuse a parser.
4. Run `cosmics tools doctor <id>` — it verifies the binary exists, the version probe runs, and the schema is valid.
5. Add a stage-allow-list entry if the tool belongs to a stage that does not yet include its category.

No Go code change is required to add a tool that uses an existing parser. That is the point of the registry.

---

## 8. Open questions

| # | Question | Impact |
|---|---|---|
| Q1 | Should the image tag be pinned by digest in `config.yaml` for strict reproducibility? | Yes for regulated engagements; default is tag-based for practicality |
| Q2 | Is `--read-only` compatible with every tool in the catalog? (some write to `/root/.cache`) | Verify per tool in M3; add targeted `tmpfs` mounts as needed |
| Q3 | Should `masscan` be enabled by default given NET_RAW unreliability? | Currently `intrusive` + caveat; consider disabling if fidelity proves poor |
| Q4 | Wordlist strategy — bake in, or mount from host? | Baked in for reproducibility; host mount is an optimization |
| Q5 | How to keep the ProjectDiscovery tools current without rebuilding? | Accept periodic rebuilds; `doctor` reports stale versions |
