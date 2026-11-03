# Contenuti realistici condivisi dai mock (repo fonlogen/ervisio-core).
# build.py li inserisce nei layout delle tre direzioni.

def ic(name, cls="i"):
    return f'<svg class="{cls}" aria-hidden="true"><use href="#i-{name}"/></svg>'

FILES = [
    ("folder", ".github/workflows", "ci: matrice arm64 per i test di storage", "3 giorni fa"),
    ("folder", "cmd/ervisiod", "cmd: flag --cache-budget con default 50 GB", "1 settimana fa"),
    ("folder", "internal", "storage: sfratta i pack freddi oltre il budget", "2 ore fa"),
    ("folder", "web", "web: allinea React a 18.3.1", "5 giorni fa"),
    ("folder", "docs", "docs: procedura di ripristino da StorageBox", "2 settimane fa"),
    ("file", ".gitignore", "build: ignora dist/ e coverage.out", "4 mesi fa"),
    ("file", "go.mod", "deps: go-git 5.13, toml 1.4", "6 giorni fa"),
    ("file", "Makefile", "build: target nfpm per deb e rpm", "1 mese fa"),
    ("file", "README.md", "docs: requisiti git 2.45 nel README", "3 settimane fa"),
    ("file", "LICENSE", "Licenza AGPL-3.0", "8 mesi fa"),
]

def file_rows(row_cls="", name_cls="", msg_cls="", time_cls=""):
    out = []
    for kind, name, msg, when in FILES:
        go = ' data-go="file"' if name == "internal" else ""
        out.append(
            f'<a href="#"{go} class="{row_cls} k-{kind}">{ic(kind)}'
            f'<span class="{name_cls}">{name}</span><span class="{msg_cls}">{msg}</span>'
            f'<span class="{time_cls}">{when}</span></a>')
    return "\n".join(out)

README = """
<h1>ervisio-core</h1>
<p>Il demone <code>ervisiod</code> e la sua console web: gestisci server Linux e Windows da un solo pannello, con plugin firmati e agenti remoti.</p>
<h2>Installazione</h2>
<pre><code>curl -fsSL https://get.ervisio.app | sh
sudo systemctl enable --now ervisiod</code></pre>
<p>Serve git 2.45 o successivo. La console risponde su <code>https://&lt;host&gt;:7443</code>.</p>
"""

GO = [
    '<span class="kw">package</span> tier',
    '',
    '<span class="co">// Evict libera spazio finché la cache calda torna sotto il budget.</span>',
    '<span class="co">// Sfratta solo i pack con una copia canonica confermata sullo storage esterno.</span>',
    '<span class="kw">func</span> (c *<span class="ty">Cache</span>) <span class="fn">Evict</span>(ctx <span class="ty">context.Context</span>) (<span class="ty">int64</span>, <span class="ty">error</span>) {',
    '\tc.mu.<span class="fn">Lock</span>()',
    '\t<span class="kw">defer</span> c.mu.<span class="fn">Unlock</span>()',
    '',
    '\t<span class="kw">var</span> freed <span class="ty">int64</span>',
    '\t<span class="kw">for</span> c.used &gt; c.budget {',
    '\t\tp := c.lru.<span class="fn">Back</span>()',
    '\t\t<span class="kw">if</span> p == <span class="kw">nil</span> {',
    '\t\t\t<span class="kw">return</span> freed, <span class="ty">ErrBudgetTooSmall</span>',
    '\t\t}',
    '\t\t<span class="kw">if</span> !p.<span class="fn">Durable</span>() {',
    '\t\t\tc.lru.<span class="fn">MoveToFront</span>(p) <span class="co">// upload ancora in corso</span>',
    '\t\t\t<span class="kw">continue</span>',
    '\t\t}',
    '\t\t<span class="kw">if</span> err := os.<span class="fn">Remove</span>(p.path); err != <span class="kw">nil</span> {',
    '\t\t\t<span class="kw">return</span> freed, fmt.<span class="fn">Errorf</span>(<span class="st">"evict %s: %w"</span>, p.name, err)',
    '\t\t}',
    '\t\tc.used -= p.size',
    '\t\tfreed += p.size',
    '\t}',
    '\t<span class="kw">return</span> freed, <span class="kw">nil</span>',
    '}',
]

def code_table(start=41):
    rows = "".join(f'<tr><td class="ln">{start+i}</td><td>{l}</td></tr>' for i, l in enumerate(GO))
    return f'<div class="code"><table>{rows}</table></div>'

DIFF = [
    ("hunk", "", "", "@@ -48,12 +48,19 @@ func (c *Cache) Evict(ctx context.Context) (int64, error) {"),
    ("ctx", "48", "48", '\tc.mu.<span class="fn">Lock</span>()'),
    ("ctx", "49", "49", '\t<span class="kw">defer</span> c.mu.<span class="fn">Unlock</span>()'),
    ("ctx", "50", "50", ''),
    ("del", "51", "", '\t<span class="kw">for</span> _, p := <span class="kw">range</span> c.packs {'),
    ("del", "52", "", '\t\t<span class="kw">if</span> c.used &lt;= c.budget { <span class="kw">break</span> }'),
    ("add", "", "51", '\t<span class="kw">var</span> freed <span class="ty">int64</span>'),
    ("add", "", "52", '\t<span class="kw">for</span> c.used &gt; c.budget {'),
    ("add", "", "53", '\t\tp := c.lru.<span class="fn">Back</span>()'),
    ("add", "", "54", '\t\t<span class="kw">if</span> p == <span class="kw">nil</span> {'),
    ("add", "", "55", '\t\t\t<span class="kw">return</span> freed, <span class="ty">ErrBudgetTooSmall</span>'),
    ("add", "", "56", '\t\t}'),
    ("add", "", "57", '\t\t<span class="kw">if</span> !p.<span class="fn">Durable</span>() {'),
    ("add", "", "58", '\t\t\tc.lru.<span class="fn">MoveToFront</span>(p)'),
    ("add", "", "59", '\t\t\t<span class="kw">continue</span>'),
    ("add", "", "60", '\t\t}'),
    ("ctx", "53", "61", '\t\t<span class="kw">if</span> err := os.<span class="fn">Remove</span>(p.path); err != <span class="kw">nil</span> {'),
    ("ctx", "54", "62", '\t\t\t<span class="kw">return</span> freed, fmt.<span class="fn">Errorf</span>(<span class="st">"evict %s: %w"</span>, p.name, err)'),
]
COMMENT_AFTER = "58"

def diff_table(comment_html=""):
    rows = []
    for kind, o, n, txt in DIFF:
        mk = {"add": "+", "del": "−", "ctx": " ", "hunk": ""}[kind]
        if kind == "hunk":
            rows.append(f'<tr class="hunk"><td class="ln"></td><td class="ln"></td><td class="mk"></td><td>{txt}</td></tr>')
            continue
        rows.append(f'<tr class="{kind}"><td class="ln">{o}</td><td class="ln">{n}</td><td class="mk">{mk}</td><td>{txt}</td></tr>')
        if n == COMMENT_AFTER and comment_html:
            rows.append(f'<tr class="cmt"><td colspan="4">{comment_html}</td></tr>')
    return f'<div class="code diff"><table>{"".join(rows)}</table></div>'

PR_FILES = [
    ("internal/storage/tier/cache.go", "+41", "−12", True),
    ("internal/storage/tier/cache_test.go", "+96", "−3", False),
    ("internal/storage/tier/lru.go", "+38", "", False),
    ("cmd/ervisiod/flags.go", "+6", "−2", False),
    ("docs/storage.md", "+3", "−20", False),
]

LOG = [
    ('m', '##[group]Run go test -race ./internal/storage/...'),
    ('', '<span class="c">go</span> test -race -count=1 ./internal/storage/...'),
    ('m', 'go: downloading github.com/go-git/go-git/v5 v5.13.1'),
    ('', '=== RUN   TestEvictRespectsBudget'),
    ('', '--- <span class="g">PASS</span>: TestEvictRespectsBudget (0.21s)'),
    ('', '=== RUN   TestEvictSkipsPendingUploads'),
    ('', '--- <span class="g">PASS</span>: TestEvictSkipsPendingUploads (0.34s)'),
    ('', '=== RUN   TestEvictBudgetTooSmall'),
    ('', '--- <span class="g">PASS</span>: TestEvictBudgetTooSmall (0.02s)'),
    ('', '=== RUN   TestColdCloneFromObjectStorage'),
    ('', '    tier_test.go:188: minio pronto su 127.0.0.1:39211'),
    ('', '    tier_test.go:201: <span class="y">primo byte dopo 41.8s (limite 90s)</span>'),
    ('', '--- <span class="g">PASS</span>: TestColdCloneFromObjectStorage (44.07s)'),
    ('', '=== RUN   TestStorageBoxReconnect'),
    ('', '    sftp_test.go:77: sshd in ascolto, prima connessione rifiutata come previsto'),
    ('', '<span class="m">▍</span>'),
]

def log_html():
    out = []
    for i, (c, t) in enumerate(LOG, 1):
        cls = f' class="{c}"' if c else ""
        out.append(f'<div class="l"><span class="n">{i}</span><span{cls}>{t}</span></div>')
    return f'<div class="log">{"".join(out)}</div>'

JOBS = [
    ("ok", "lint", "38 s"),
    ("ok", "test (linux/amd64)", "2 min 51 s"),
    ("run", "test (linux/arm64)", "3 min 05 s"),
    ("wait", "build", ""),
    ("wait", "docker push", ""),
]

def job_icon(state):
    return {"ok": ic("check", "i st-ok"), "run": ic("redo", "i st-run spin"),
            "wait": ic("clock", "i faint"), "err": ic("x", "i st-err")}[state]

TAGS = [
    ("0.5.2", "latest", "sha256:9f2c1e7a", "75,3 MB", "2 ore fa"),
    ("0.5.1", "", "sha256:41bd08c3", "75,1 MB", "12 giorni fa"),
    ("0.5.0", "", "sha256:e07a5f19", "74,8 MB", "1 mese fa"),
    ("edge", "", "sha256:c3a99d02", "75,4 MB", "40 minuti fa"),
]

LAYERS = [
    ("debian:bookworm-slim", "28,2 MB", 37),
    ("apt-get install git ca-certificates", "21,6 MB", 29),
    ("COPY ervisiod /usr/bin/ervisiod", "24,9 MB", 33),
    ("COPY web/dist /usr/share/ervisio/web", "0,6 MB", 1),
]
