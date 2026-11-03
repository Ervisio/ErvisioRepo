#!/usr/bin/env python3
"""Genera i mock delle tre direzioni (fragment per Variant Studio) in design/mock/ e nel round."""
import os, sys, shutil
sys.path.insert(0, os.path.dirname(__file__))
from content import *  # noqa

HERE = os.path.dirname(os.path.abspath(__file__))
ROUND = os.path.join(HERE, "..", ".variant-studio", "rounds", "001-direzione-visiva")

SECTIONS = [  # id schermata, etichetta, icona, classe hue, conteggio
    ("home", "Codice", "code", "sec-code", ""),
    ("diff", "Pull request", "pr", "sec-pr", "7"),
    ("issue", "Issue", "issue", "sec-issue", "23"),
    ("actions", "Actions", "play", "sec-act", ""),
    ("pacchetto", "Pacchetti", "pkg", "sec-pkg", "4"),
]

def nav_items(item_cls, tile_cls=None, show_count=True):
    out = []
    for go, label, icon, sec, n in SECTIONS:
        g = f' data-go="{go}"' if go != "issue" else ""
        tile = f'<span class="{tile_cls}">{ic(icon)}</span>' if tile_cls else ic(icon)
        cnt = f'<em>{n}</em>' if (n and show_count) else ""
        out.append(f'<a href="#"{g} class="{item_cls} {sec}">{tile}<span>{label}</span>{cnt}</a>')
    return "".join(out)

def checks_row():
    return "".join(f'<div class="jobrow">{job_icon(s)}<span>{n}</span><span class="faint">{d}</span></div>' for s, n, d in JOBS)

COMMENT = f"""<div class="thread">
  <div class="tc"><span class="av av-mb">MB</span><div><b>marta.b</b> <span class="faint">1 ora fa</span>
  <p>Se tutti i pack in coda sono in upload questo ciclo non termina: <code>MoveToFront</code> li rimette davanti e <code>Back()</code> restituisce sempre lo stesso. Serve un limite di giri o un controllo su <code>c.pending</code>.</p></div></div>
  <div class="tc"><span class="av av-rc">RC</span><div><b>riccardo</b> <span class="faint">40 minuti fa</span>
  <p>Giusto. Aggiungo un contatore e restituisco <code>ErrAllPending</code> dopo un giro completo.</p></div></div>
  <div class="treply">Rispondi…</div>
</div>"""

# ---------------------------------------------------------------- A: Console
def variant_a():
    css = """
<style>
.a { --hue: var(--sec-code); --hue-s: var(--sec-code-s); display: grid; grid-template-columns: 72px 248px 1fr; min-height: 100vh; gap: 0 12px; padding: 12px 12px 12px 0; }
.a-rail { display: flex; flex-direction: column; align-items: center; gap: 8px; padding-top: 4px; }
.a-logo { width: 44px; height: 44px; border-radius: 14px; background: var(--ink); color: #000; display: grid; place-items: center; font: 800 20px var(--font); margin-bottom: 10px; }
.a-ri { width: 44px; height: 44px; border-radius: 50%; display: grid; place-items: center; color: var(--ink2); background: var(--surface); transition: border-radius .18s, background .18s; position: relative; }
.a-ri:hover, .a-ri.on { border-radius: 14px; background: var(--sunk); color: var(--ink); }
.a-ri.on::before { content: ""; position: absolute; left: -14px; width: 4px; height: 24px; border-radius: 0 4px 4px 0; background: var(--ink); }
.a-ri .i { width: 19px; height: 19px; }
.a-ri.tile { font: 800 13px var(--font); color: #000; }
.a-sp { flex: 1; }
.a-nav { display: flex; flex-direction: column; gap: 4px; padding-top: 6px; }
.a-repo { padding: 4px 10px 14px; }
.a-repo small { color: var(--ink2); font-weight: 600; font-size: 13px; }
.a-repo b { display: block; font-size: 18px; font-weight: 800; letter-spacing: -.01em; }
.a-chip { display: inline-flex; align-items: center; gap: 5px; height: 22px; padding: 0 8px; border-radius: 999px; background: var(--sunk); font-size: 12px; font-weight: 700; color: var(--ink2); margin-top: 6px; }
.a-chip .i { width: 12px; height: 12px; }
.a-ni { display: flex; align-items: center; gap: 11px; height: 44px; padding: 0 10px 0 6px; border-radius: 12px; font-weight: 700; color: var(--ink2); }
.a-ni:hover { background: var(--surface); color: var(--ink); }
.a-ni[aria-current] { background: var(--sunk); color: var(--ink); }
.a-ni em { margin-left: auto; font-style: normal; font-size: 12px; color: var(--ink3); }
.a-kt { width: 32px; height: 32px; border-radius: 11px; display: grid; place-items: center; background: var(--hue-s); color: var(--hue); flex: none; }
.a-ni[aria-current] .a-kt { background: var(--hue); color: #000; }
.a-store { margin-top: 14px; background: var(--surface); border-radius: 18px; padding: 14px; font-size: 12.5px; color: var(--ink2); display: flex; flex-direction: column; gap: 6px; }
.a-store b { color: var(--ink); font-size: 13px; }
.a-meter { height: 6px; border-radius: 6px; background: var(--sunk); overflow: hidden; }
.a-meter i { display: block; height: 100%; width: 34%; background: var(--ok); border-radius: 6px; }
.a-main { background: var(--surface); border-radius: 20px; padding: 20px 22px 28px; min-width: 0; }
.a-grid > div, .b-cols > div, .c-body > div { min-width: 0; }
.a-main > section { display: none; }
.a-head { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-bottom: 14px; }
.a-head h1 { margin: 0 6px 0 0; font-size: 26px; font-weight: 800; letter-spacing: -.02em; }
.a-head .sub { color: var(--ink2); font-size: 13px; }
.a-grow { flex: 1; }
.a-btn { display: inline-flex; align-items: center; gap: 7px; height: 36px; padding: 0 14px; border-radius: 12px; background: var(--sunk); font-weight: 700; font-size: 13px; }
.a-btn:hover { background: var(--raise); }
.a-btn.pri { background: var(--hue); color: #000; }
.a-btn em { font-style: normal; color: var(--ink2); font-weight: 600; }
.a-grid { display: grid; grid-template-columns: 1fr 280px; gap: 12px; align-items: start; }
.a-card { background: var(--sunk); border-radius: 18px; padding: 6px; min-width: 0; }
.a-card + .a-card { margin-top: 12px; }
.a-ch { display: flex; align-items: center; gap: 10px; padding: 8px 10px 8px; font-size: 13px; flex-wrap: wrap; }
.a-ch h3 { margin: 0; font-size: 14px; font-weight: 800; display: flex; gap: 8px; align-items: center; }
.a-ch h3 .i { color: var(--hue); }
.a-row { display: grid; grid-template-columns: 18px minmax(120px, 220px) 1fr auto; gap: 12px; align-items: center; padding: 8px 10px; border-radius: 12px; font-size: 13.5px; }
.a-row:hover { background: var(--raise); }
.a-row.k-folder .i { color: var(--hue); }
.a-row.k-file .i { color: var(--ink3); }
.a-row .nm { font-weight: 700; }
.a-row .msg { color: var(--ink2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.a-row .tm { color: var(--ink3); font-size: 12.5px; white-space: nowrap; }
.a-last { display: flex; align-items: center; gap: 10px; padding: 10px; border-radius: 12px; background: var(--raise); margin: 0 0 4px; font-size: 13px; }
.a-last .msg { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.a-md { padding: 4px 18px 14px; font-size: 14px; line-height: 1.6; max-width: 72ch; }
.a-md h1 { font-size: 22px; font-weight: 800; margin: 8px 0; } .a-md h2 { font-size: 16px; font-weight: 800; margin: 18px 0 6px; }
.a-md code { font: 12.5px var(--mono); background: var(--raise); padding: 1px 5px; border-radius: 6px; }
.a-md pre { background: var(--bg); border-radius: 12px; padding: 12px 14px; overflow-x: auto; }
.a-md pre code { background: none; padding: 0; }
.a-side { display: flex; flex-direction: column; gap: 12px; }
.a-kv { display: grid; grid-template-columns: auto 1fr; gap: 8px 14px; padding: 4px 10px 10px; margin: 0; font-size: 13px; }
.a-kv dt { color: var(--ink2); } .a-kv dd { margin: 0; font-weight: 600; text-align: right; }
.a-lang { display: flex; height: 8px; border-radius: 8px; overflow: hidden; gap: 2px; margin: 4px 10px 8px; }
.a-lang i:nth-child(1) { flex: 71; background: var(--h-file); } .a-lang i:nth-child(2) { flex: 24; background: var(--h-svc); } .a-lang i:nth-child(3) { flex: 5; background: var(--ink3); }
.a-crumb { display: flex; align-items: center; gap: 4px; flex-wrap: wrap; font-weight: 700; font-size: 15px; }
.a-crumb a { color: var(--hue); } .a-crumb .i { color: var(--ink3); width: 14px; }
.a-code { background: var(--bg); border-radius: 12px; padding: 10px 0; margin: 0 0 0; }
.jobrow { display: flex; gap: 10px; align-items: center; padding: 7px 10px; font-size: 13px; border-radius: 12px; }
.jobrow span:nth-child(2) { flex: 1; font-weight: 600; }
.a-tabs { display: flex; gap: 4px; background: var(--sunk); padding: 4px; border-radius: 14px; width: fit-content; margin-bottom: 12px; flex-wrap: wrap; }
.a-tab { height: 32px; padding: 0 12px; border-radius: 10px; display: inline-flex; align-items: center; gap: 6px; font-size: 13px; font-weight: 700; color: var(--ink2); }
.a-tab.on { background: var(--raise); color: var(--ink); }
.a-tab em { font-style: normal; color: var(--ink3); }
.a-state { display: inline-flex; align-items: center; gap: 6px; height: 28px; padding: 0 11px; border-radius: 999px; background: var(--hue-s); color: var(--hue); font-size: 12.5px; font-weight: 800; }
.a-split { display: grid; grid-template-columns: 240px 1fr; gap: 12px; align-items: start; }
.a-ft { display: flex; flex-direction: column; gap: 2px; }
.a-ft a { display: flex; gap: 8px; align-items: center; padding: 7px 10px; border-radius: 10px; font: 12px var(--mono); color: var(--ink2); }
.a-ft a.on { background: var(--raise); color: var(--ink); }
.a-ft a span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.a-ft .p { color: var(--ok); } .a-ft .m { color: var(--err); }
.a-fh { display: flex; align-items: center; gap: 10px; padding: 8px 10px; font: 600 12.5px var(--mono); }
.a-fh .p { color: var(--ok); } .a-fh .m { color: var(--err); }
.diff tr.cmt td { padding: 8px 12px; background: var(--sunk); white-space: normal; font-family: var(--font); }
.thread { background: var(--surface); border-radius: 14px; padding: 12px 14px; max-width: 720px; display: flex; flex-direction: column; gap: 10px; font-size: 13.5px; }
.tc { display: flex; gap: 10px; } .tc p { margin: 3px 0 0; color: var(--ink); line-height: 1.5; }
.tc code { font: 12px var(--mono); background: var(--raise); padding: 1px 5px; border-radius: 6px; }
.treply { background: var(--sunk); border-radius: 12px; padding: 9px 12px; color: var(--ink3); }
.a-jobs { display: flex; flex-direction: column; gap: 2px; padding: 4px; }
.a-jobs .jobrow.on { background: var(--raise); }
.a-log { background: var(--bg); border-radius: 12px; padding: 10px 0; }
.a-pull { display: flex; align-items: center; gap: 10px; background: var(--bg); border-radius: 12px; padding: 10px 12px; margin: 0 4px 6px; font: 13px var(--mono); }
.a-pull span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.a-tag { display: grid; grid-template-columns: 90px 1fr auto auto; gap: 12px; align-items: center; padding: 9px 10px; border-radius: 12px; font-size: 13px; }
.a-tag:hover { background: var(--raise); }
.a-tag b { font: 600 13px var(--mono); }
.a-tag .lt { font-size: 11.5px; font-weight: 800; color: var(--hue); background: var(--hue-s); padding: 2px 8px; border-radius: 999px; justify-self: start; }
.a-layer { display: grid; grid-template-columns: 1fr 70px; gap: 4px 12px; padding: 6px 10px; font-size: 12.5px; }
.a-layer code { font: 12px var(--mono); color: var(--ink2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.a-layer span { text-align: right; color: var(--ink2); }
.a-layer i { grid-column: 1 / -1; height: 5px; border-radius: 5px; background: var(--hue-s); }
.a-layer i::before { content: ""; display: block; height: 100%; width: var(--w); background: var(--hue); border-radius: 5px; }
.a-top { display: none; }
@media (max-width: 900px) {
  .a { grid-template-columns: minmax(0,1fr); padding: 0; gap: 0; }
  .a-rail, .a-nav { display: none; }
  .a-top { display: block; padding: 12px 12px 8px; }
  .a-top .tb { display: flex; align-items: center; gap: 10px; }
  .a-top .tb b { font-size: 17px; font-weight: 800; flex: 1; }
  .a-top .tb small { color: var(--ink2); font-weight: 600; }
  .a-chips { display: flex; gap: 6px; overflow-x: auto; padding: 10px 0 2px; scrollbar-width: none; }
  .a-chips .a-ni { height: 38px; flex: none; padding: 0 12px 0 4px; background: var(--surface); }
  .a-chips .a-kt { width: 28px; height: 28px; border-radius: 9px; }
  .a-main { border-radius: 20px 20px 0 0; padding: 16px 12px 24px; }
  .a-grid, .a-split { grid-template-columns: minmax(0,1fr); }
  .a-grid > *, .a-split > * { min-width: 0; }
  .a-row { grid-template-columns: 18px 1fr auto; } .a-row .msg { display: none; }
  .a-head h1 { font-size: 22px; }
  .a-ft { flex-direction: row; overflow-x: auto; } .a-ft a { flex: none; max-width: 240px; }
  .a-tag { grid-template-columns: 70px 1fr auto; } .a-tag .sz { display: none; }
  .a-hide-m { display: none !important; }
}
</style>"""
    rail = f"""<nav class="a-rail" aria-label="Ervisio">
  <div class="a-logo">E</div>
  <a href="#" class="a-ri" title="Dashboard" aria-label="Dashboard">{ic("grid")}</a>
  <a href="#" class="a-ri on" title="Repository" aria-label="Repository" aria-current="true">{ic("book")}</a>
  <a href="#" class="a-ri" title="Revisioni" aria-label="Revisioni">{ic("pr")}</a>
  <a href="#" class="a-ri" title="Notifiche" aria-label="Notifiche">{ic("bell")}</a>
  <a href="#" class="a-ri tile" style="background:var(--h-usr)" title="fonlogen/ervisio-core" aria-label="fonlogen/ervisio-core">EC</a>
  <a href="#" class="a-ri tile" style="background:var(--h-plg)" title="fonlogen/ervisio-drive" aria-label="fonlogen/ervisio-drive">ED</a>
  <a href="#" class="a-ri tile" style="background:var(--h-log)" title="fonlogen/ervisio-logs" aria-label="fonlogen/ervisio-logs">EL</a>
  <a href="#" class="a-ri" title="Nuovo repository" aria-label="Nuovo repository">{ic("plus")}</a>
  <div class="a-sp"></div>
  <span class="av lg av-rc">RC</span>
</nav>"""
    nav = f"""<aside class="a-nav">
  <div class="a-repo"><small>fonlogen /</small><b>ervisio-core</b><span class="a-chip">{ic("lock")} Privato</span></div>
  {nav_items("a-ni", "a-kt")}
  <a href="#" class="a-ni">{'<span class="a-kt" style="background:var(--sunk);color:var(--ink2)">' + ic("gear") + '</span>'}<span>Impostazioni</span></a>
  <div class="a-store"><b>Storage</b><div class="a-meter"><i></i></div><span>412 MB in cache calda su 1,2 GB</span><span>Copia canonica su Hetzner Object Storage, allineata 2 minuti fa</span></div>
</aside>"""
    top = f"""<div class="a-top"><div class="tb">{ic("menu")}<b><small>fonlogen / </small>ervisio-core</b><span class="av lg av-rc">RC</span></div>
<div class="a-chips">{nav_items("a-ni", "a-kt", False)}</div></div>"""

    home = f"""<section data-s="home" class="sec-code">
  <div class="a-head"><h1>Codice</h1>
    <a href="#" class="a-btn">{ic("branch")} main {ic("down")}</a>
    <span class="sub a-hide-m">312 commit, 14 branch, 38 tag</span><span class="a-grow"></span>
    <a href="#" class="a-btn a-hide-m">{ic("star")} Stella <em>48</em></a>
    <a href="#" class="a-btn pri">{ic("copy")} Clona</a></div>
  <div class="a-grid"><div>
    <div class="a-card">
      <div class="a-last"><span class="av av-rc">RC</span><b>riccardo</b><span class="msg dim">storage: sfratta i pack freddi oltre il budget</span>{ic("check","i st-ok")}<span class="mono faint">a3f9c21</span><span class="faint a-hide-m">2 ore fa</span></div>
      {file_rows("a-row","nm","msg","tm")}
    </div>
    <div class="a-card"><div class="a-ch"><h3>{ic("book")} README.md</h3></div><div class="a-md">{README}</div></div>
  </div>
  <div class="a-side">
    <div class="a-card"><div class="a-ch"><h3>Informazioni</h3></div>
      <p style="margin:0 10px 10px;font-size:13px;color:var(--ink2)">Il demone ervisiod: console web, plugin e agenti per server Linux e Windows.</p>
      <div class="a-lang"><i></i><i></i><i></i></div>
      <dl class="a-kv"><dt>Go</dt><dd>71%</dd><dt>TypeScript</dt><dd>24%</dd><dt>Ultima release</dt><dd>v0.5.2</dd><dt>Licenza</dt><dd>AGPL-3.0</dd></dl></div>
    <div class="a-card sec-act"><div class="a-ch"><h3>{ic("play")} ci.yml #1287</h3><span class="st-run" style="margin-left:auto;font-weight:700;font-size:12.5px">In corso</span></div>{checks_row()}</div>
  </div></div>
</section>"""

    file = f"""<section data-s="file" class="sec-code">
  <div class="a-head"><div class="a-crumb"><a href="#" data-go="home">ervisio-core</a>{ic("right")}<a href="#">internal</a>{ic("right")}<a href="#">storage</a>{ic("right")}<a href="#">tier</a>{ic("right")}<span>cache.go</span></div><span class="a-grow"></span>
    <a href="#" class="a-btn">{ic("branch")} main</a></div>
  <div class="a-card">
    <div class="a-last"><span class="av av-rc">RC</span><b>riccardo</b><span class="msg dim">storage: sfratta i pack freddi oltre il budget</span><span class="mono faint">a3f9c21</span></div>
    <div class="a-ch"><span class="dim">142 righe, 4,1 KB</span><span class="a-grow"></span>
      <a href="#" class="a-btn">Blame</a><a href="#" class="a-btn a-hide-m">Cronologia</a><a href="#" class="a-btn a-hide-m">Raw</a><a href="#" class="a-btn">Modifica</a></div>
    <div class="a-code">{code_table()}</div>
  </div>
</section>"""

    pr_ft = "".join(f'<a href="#" class="{"on" if on else ""}">{ic("file")}<span>{f.split("/")[-1]}</span><b class="p">{p}</b><b class="m">{m}</b></a>' for f, p, m, on in PR_FILES)
    diff = f"""<section data-s="diff" class="sec-pr">
  <div class="a-head"><h1>Cache calda: sfratto LRU dei pack freddi <span class="faint" style="font-weight:600">#412</span></h1></div>
  <div class="a-head" style="margin-top:-6px"><span class="a-state">{ic("pr")} Aperta</span><span class="sub"><b style="color:var(--ink)">riccardo</b> vuole unire 3 commit da <span class="mono">feat/lru-evict</span> in <span class="mono">main</span></span><span class="a-grow"></span><a href="#" class="a-btn pri">Invia revisione</a></div>
  <div class="a-tabs"><a class="a-tab" href="#">{ic("chat")} Conversazione <em>6</em></a><a class="a-tab" href="#">{ic("commit")} Commit <em>3</em></a><a class="a-tab" href="#">{ic("check")} Controlli <em>3/5</em></a><a class="a-tab on" href="#">{ic("file")} File <em>5</em></a></div>
  <div class="a-split">
    <div class="a-card a-hide-m"><div class="a-ch"><h3>File modificati</h3><span class="mono"><span class="st-ok">+184</span> <span class="st-err">−37</span></span></div><div class="a-ft">{pr_ft}</div></div>
    <div class="a-card"><div class="a-fh">{ic("file")}<span style="flex:1">internal/storage/tier/cache.go</span><span class="p">+41</span><span class="m">−12</span></div>
      <div class="a-code" style="padding:0;overflow:hidden">{diff_table(COMMENT)}</div></div>
  </div>
</section>"""

    jobs = "".join(f'<div class="jobrow {"on" if s=="run" else ""}">{job_icon(s)}<span>{n}</span><span class="faint">{d}</span></div>' for s, n, d in JOBS)
    act = f"""<section data-s="actions" class="sec-act">
  <div class="a-head"><h1>storage: sfratta i pack freddi oltre il budget</h1></div>
  <div class="a-head" style="margin-top:-6px"><span class="a-state">{ic("redo","i spin")} In corso</span><span class="sub"><span class="mono">ci.yml</span> run #1287, push su <span class="mono">main</span> di riccardo, avviato 3 minuti fa</span><span class="a-grow"></span><a href="#" class="a-btn">Annulla</a></div>
  <div class="a-split">
    <div class="a-card"><div class="a-ch"><h3>Job</h3><span class="faint">2 di 5 completati</span></div><div class="a-jobs">{jobs}</div></div>
    <div class="a-card"><div class="a-ch"><h3>{ic("term")} test (linux/arm64)</h3><span class="faint">runner hz-arm-02, Firecracker</span><span class="a-grow"></span><a href="#" class="a-btn">Log completo</a></div>
      <div class="a-log">{log_html()}</div></div>
  </div>
</section>"""

    tags = "".join(f'<div class="a-tag"><b>{t}</b>{"<span class=lt>"+l+"</span>" if l else "<span></span>"}<span class="mono faint sz">{d}</span><span class="faint">{w}</span></div>' for t, l, d, s, w in TAGS)
    layers = "".join(f'<div class="a-layer"><code>{c}</code><span>{s}</span><i style="--w:{p}%"></i></div>' for c, s, p in LAYERS)
    pkg = f"""<section data-s="pacchetto" class="sec-pkg">
  <div class="a-head"><h1>ervisiod</h1><span class="a-state">{ic("pkg")} Immagine OCI</span><span class="a-grow"></span><a href="#" class="a-btn a-hide-m">Impostazioni pacchetto</a></div>
  <div class="a-grid"><div>
    <div class="a-card"><div class="a-ch"><h3>Scarica</h3></div><div class="a-pull"><span>docker pull git.ervisio.app/fonlogen/ervisiod:0.5.2</span>{ic("copy")}</div></div>
    <div class="a-card"><div class="a-ch"><h3>{ic("tag")} Tag</h3><span class="faint">4 tag, 19 versioni</span></div>{tags}</div>
    <div class="a-card"><div class="a-ch"><h3>{ic("layers")} Layer di linux/amd64</h3><span class="faint">75,3 MB compressi</span></div>{layers}</div>
  </div><div class="a-side">
    <div class="a-card"><div class="a-ch"><h3>Dettagli</h3></div><dl class="a-kv"><dt>Digest</dt><dd class="mono">9f2c1e7a</dd><dt>Piattaforme</dt><dd>amd64, arm64</dd><dt>Download</dt><dd>2.318</dd><dt>Firmata</dt><dd class="st-ok">cosign</dd><dt>Sorgente</dt><dd>ervisio-core</dd></dl></div>
    <div class="a-card"><div class="a-ch"><h3>Storage</h3></div><p style="margin:0 10px 12px;font-size:13px;color:var(--ink2);line-height:1.5">Blob su Hetzner Object Storage. I 3 tag più recenti restano in cache calda.</p></div>
  </div></div>
</section>"""
    return css + f'<div class="a">{rail}{nav}<main class="a-main">{top}{home}{file}{diff}{act}{pkg}</main></div>'


# ---------------------------------------------------------------- B: Foglio
def variant_b():
    css = """
<style>
.b { --hue: var(--sec-code); --hue-s: var(--sec-code-s); max-width: 1180px; margin: 0 auto; padding: 0 28px 60px; font-size: 14.5px; }
.b-bar { display: flex; align-items: center; gap: 16px; height: 64px; }
.b-mark { font-weight: 800; font-size: 16px; letter-spacing: -.01em; display: flex; gap: 8px; align-items: center; }
.b-mark i { width: 22px; height: 22px; border-radius: 7px; background: var(--ink); display: inline-block; }
.b-mark span { color: var(--ink3); font-weight: 600; }
.b-search { flex: 1; max-width: 420px; margin-left: auto; height: 38px; border-radius: 999px; background: var(--surface); display: flex; align-items: center; gap: 8px; padding: 0 14px; color: var(--ink3); font-size: 13.5px; }
.b-search kbd { margin-left: auto; font: 600 11px var(--mono); background: var(--sunk); padding: 2px 6px; border-radius: 6px; color: var(--ink2); }
.b-mast { padding: 34px 0 6px; }
.b-own { color: var(--ink2); font-weight: 600; font-size: 15px; display: flex; gap: 8px; align-items: center; }
.b-own .pv { font-size: 12px; font-weight: 700; color: var(--ink2); background: var(--surface); border-radius: 999px; padding: 2px 9px; display: inline-flex; gap: 4px; align-items: center; }
.b-own .pv .i { width: 12px; height: 12px; }
.b-name { font-size: 46px; font-weight: 800; letter-spacing: -.035em; line-height: 1.05; margin: 4px 0 10px; }
.b-desc { color: var(--ink2); font-size: 16px; max-width: 60ch; margin: 0; }
.b-tabs { display: flex; gap: 6px; margin: 26px 0 30px; overflow-x: auto; scrollbar-width: none; }
.b-tab { display: inline-flex; align-items: center; gap: 8px; height: 40px; padding: 0 16px; border-radius: 999px; color: var(--ink2); font-weight: 700; flex: none; }
.b-tab .i { color: var(--hue); }
.b-tab em { font-style: normal; font-size: 12.5px; color: var(--ink3); }
.b-tab:hover { background: var(--surface); color: var(--ink); }
.b-tab[aria-current] { background: var(--hue-s); color: var(--hue); }
.b-tab[aria-current] em { color: var(--hue); }
.b-cols { display: grid; grid-template-columns: minmax(0, 1fr) 280px; gap: 48px; align-items: start; }
.b-sheet { background: var(--surface); border-radius: 20px; padding: 8px; }
.b-sheet + .b-sheet, .b-gap { margin-top: 20px; }
.b-strip { display: flex; align-items: center; gap: 12px; padding: 0 0 14px; flex-wrap: wrap; }
.b-strip h2 { margin: 0; font-size: 20px; font-weight: 800; letter-spacing: -.015em; }
.b-btn { display: inline-flex; align-items: center; gap: 7px; height: 38px; padding: 0 16px; border-radius: 12px; background: var(--surface); font-weight: 700; font-size: 13.5px; }
.b-btn:hover { background: var(--sunk); }
.b-btn.pri { background: var(--hue); color: #000; }
.b-grow { flex: 1; }
.b-row { display: grid; grid-template-columns: 18px minmax(130px, 210px) 1fr auto; gap: 14px; align-items: center; padding: 10px 14px; border-radius: 12px; }
.b-row:hover { background: var(--sunk); }
.b-row.k-folder .i { color: var(--hue); } .b-row.k-file .i { color: var(--ink3); }
.b-row .nm { font-weight: 700; }
.b-row .msg { color: var(--ink2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-size: 13.5px; }
.b-row .tm { color: var(--ink3); font-size: 13px; white-space: nowrap; }
.b-commit { display: flex; align-items: center; gap: 10px; padding: 12px 14px 14px; font-size: 13.5px; }
.b-commit .msg { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--ink2); }
.b-md { font-size: 15.5px; line-height: 1.65; max-width: 70ch; padding: 6px 4px; }
.b-md h1 { font-size: 30px; font-weight: 800; letter-spacing: -.02em; margin: 0 0 8px; }
.b-md h2 { font-size: 19px; font-weight: 800; margin: 28px 0 8px; }
.b-md code { font: 13px var(--mono); background: var(--surface); padding: 2px 6px; border-radius: 6px; }
.b-md pre { background: var(--surface); border-radius: 18px; padding: 16px 18px; overflow-x: auto; }
.b-md pre code { background: none; padding: 0; }
.b-margin { display: flex; flex-direction: column; gap: 28px; font-size: 13.5px; padding-top: 4px; }
.b-margin h4 { margin: 0 0 10px; font-size: 13.5px; font-weight: 800; }
.b-margin p { margin: 0; color: var(--ink2); line-height: 1.55; }
.b-kv { display: grid; grid-template-columns: auto 1fr; gap: 8px 12px; margin: 0; }
.b-kv dt { color: var(--ink2); } .b-kv dd { margin: 0; text-align: right; font-weight: 600; }
.b-lang { display: flex; height: 6px; gap: 3px; margin-bottom: 10px; }
.b-lang i { border-radius: 6px; } .b-lang i:nth-child(1) { flex: 71; background: var(--h-file); } .b-lang i:nth-child(2) { flex: 24; background: var(--h-svc); } .b-lang i:nth-child(3) { flex: 5; background: var(--ink3); }
.b-path { font: 600 15px var(--mono); display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
.b-path a { color: var(--ink2); } .b-path a:hover { color: var(--hue); } .b-path .sep { color: var(--ink3); }
.b-path b { color: var(--ink); }
.b-codebox { background: var(--surface); border-radius: 20px; padding: 14px 0; }
.b-codebox .code { font-size: 13.5px; line-height: 1.75; }
.jobrow { display: flex; gap: 10px; align-items: center; padding: 6px 0; }
.jobrow span:nth-child(2) { flex: 1; font-weight: 600; }
.b-prt { font-size: 32px; font-weight: 800; letter-spacing: -.025em; line-height: 1.15; margin: 0 0 10px; max-width: 26ch; }
.b-prt span { color: var(--ink3); font-weight: 600; }
.b-pill { display: inline-flex; align-items: center; gap: 6px; height: 30px; padding: 0 12px; border-radius: 999px; background: var(--hue-s); color: var(--hue); font-weight: 800; font-size: 13px; }
.b-meta { display: flex; gap: 12px; align-items: center; flex-wrap: wrap; color: var(--ink2); margin-bottom: 24px; }
.b-meta .mono { color: var(--ink); background: var(--surface); padding: 2px 7px; border-radius: 6px; }
.b-file { background: var(--surface); border-radius: 20px; overflow: hidden; }
.b-file + .b-file { margin-top: 16px; }
.b-fh { display: flex; gap: 10px; align-items: center; padding: 14px 18px; font: 600 13px var(--mono); }
.b-fh .p { color: var(--ok); } .b-fh .m { color: var(--err); }
.b-file .code { font-size: 13px; }
.b-fold { padding: 14px 18px; color: var(--ink2); font-size: 13.5px; display: flex; gap: 10px; align-items: center; }
.diff tr.cmt td { padding: 12px 18px; background: var(--sunk); white-space: normal; font-family: var(--font); }
.thread { display: flex; flex-direction: column; gap: 14px; font-size: 14px; max-width: 680px; }
.tc { display: flex; gap: 10px; } .tc p { margin: 4px 0 0; line-height: 1.55; }
.tc code { font: 12.5px var(--mono); background: var(--surface); padding: 1px 5px; border-radius: 6px; }
.treply { background: var(--surface); border-radius: 12px; padding: 10px 14px; color: var(--ink3); }
.b-filelist a { display: flex; gap: 8px; padding: 6px 0; font: 12.5px var(--mono); color: var(--ink2); }
.b-filelist a span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.b-filelist a.on { color: var(--hue); }
.b-filelist .p { color: var(--ok); } .b-filelist .m { color: var(--err); }
.b-jobs { display: flex; gap: 8px; flex-wrap: wrap; margin-bottom: 18px; }
.b-job { display: inline-flex; gap: 8px; align-items: center; height: 40px; padding: 0 14px; border-radius: 12px; background: var(--surface); font-weight: 700; font-size: 13.5px; }
.b-job.on { background: var(--hue-s); color: var(--hue); }
.b-job .faint { font-weight: 600; }
.b-logbox { background: var(--surface); border-radius: 20px; padding: 14px 0; }
.b-logbox .log { font-size: 13px; }
.b-logh { display: flex; gap: 10px; align-items: center; padding: 0 18px 12px; font-weight: 800; }
.b-pull { background: var(--surface); border-radius: 18px; padding: 16px 18px; font: 14px var(--mono); display: flex; gap: 12px; align-items: center; }
.b-pull span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.b-tag { display: grid; grid-template-columns: 100px 1fr auto auto; gap: 14px; align-items: center; padding: 12px 14px; border-radius: 12px; }
.b-tag:hover { background: var(--surface); }
.b-tag b { font: 600 14px var(--mono); }
.b-tag .lt { font-size: 12px; font-weight: 800; color: var(--hue); background: var(--hue-s); padding: 2px 9px; border-radius: 999px; justify-self: start; }
.b-stack { display: flex; height: 44px; gap: 3px; border-radius: 14px; overflow: hidden; margin: 6px 0 12px; }
.b-stack i { background: var(--hue-s); flex: var(--w); }
.b-stack i:nth-child(3) { background: var(--hue); }
.b-lay { display: grid; grid-template-columns: 1fr auto; gap: 8px 16px; font-size: 13.5px; }
.b-lay code { font: 13px var(--mono); color: var(--ink2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
@media (max-width: 900px) {
  .b { padding: 0 16px 40px; font-size: 14px; }
  .b-search { display: none; } .b-bar { justify-content: space-between; }
  .b-mast { padding-top: 16px; } .b-name { font-size: 34px; }
  .b-tabs { margin: 18px -16px 20px; padding: 0 16px; }
  .b-cols { grid-template-columns: minmax(0,1fr); gap: 28px; }
  .b-row { grid-template-columns: 18px 1fr auto; } .b-row .msg { display: none; }
  .b-prt { font-size: 24px; }
  .b-tag { grid-template-columns: 70px 1fr auto; } .b-tag .sz { display: none; }
  .b-hide-m { display: none !important; }
}
</style>"""
    bar = f"""<header class="b-bar"><div class="b-mark"><i></i>ervisio <span>repo</span></div><div class="b-search">{ic("search")} Cerca in ervisio-core<kbd>/</kbd></div><span class="av lg av-rc">RC</span></header>"""
    mast = f"""<div class="b-mast"><div class="b-own">fonlogen <span class="pv">{ic("lock")} Privato</span></div><div class="b-name">ervisio-core</div>
<p class="b-desc">Il demone ervisiod: console web, plugin e agenti per server Linux e Windows.</p></div>
<nav class="b-tabs">{nav_items("b-tab")}</nav>"""
    home = f"""<section data-s="home" class="sec-code"><div class="b-cols"><div>
  <div class="b-strip"><a href="#" class="b-btn">{ic("branch")} main {ic("down")}</a><span class="dim b-hide-m">312 commit</span><span class="b-grow"></span><a href="#" class="b-btn b-hide-m">{ic("star")} 48</a><a href="#" class="b-btn pri">{ic("copy")} Clona</a></div>
  <div class="b-sheet"><div class="b-commit"><span class="av av-rc">RC</span><b>riccardo</b><span class="msg">storage: sfratta i pack freddi oltre il budget</span>{ic("check","i st-ok")}<span class="mono faint">a3f9c21</span><span class="faint b-hide-m">2 ore fa</span></div>
  {file_rows("b-row","nm","msg","tm")}</div>
  <div class="b-md b-gap">{README}</div>
</div>
<aside class="b-margin">
  <div><h4>Linguaggi</h4><div class="b-lang"><i></i><i></i><i></i></div><dl class="b-kv"><dt>Go</dt><dd>71%</dd><dt>TypeScript</dt><dd>24%</dd><dt>Altro</dt><dd>5%</dd></dl></div>
  <div class="sec-act"><h4>Ultimo run</h4>{checks_row()}</div>
  <div><h4>Storage</h4><p>412 MB in cache calda. La copia canonica sta su Hetzner Object Storage, allineata 2 minuti fa.</p></div>
  <div><h4>Release</h4><dl class="b-kv"><dt>v0.5.2</dt><dd>2 ore fa</dd><dt>v0.5.1</dt><dd>12 giorni fa</dd></dl></div>
</aside></div></section>"""
    file = f"""<section data-s="file" class="sec-code">
  <div class="b-strip"><div class="b-path"><a href="#" data-go="home">ervisio-core</a><span class="sep">/</span><a href="#">internal</a><span class="sep">/</span><a href="#">storage</a><span class="sep">/</span><a href="#">tier</a><span class="sep">/</span><b>cache.go</b></div></div>
  <div class="b-strip"><span class="av av-rc">RC</span><span class="dim">riccardo, 2 ore fa</span><span class="mono faint">a3f9c21</span><span class="b-grow"></span><span class="dim b-hide-m">142 righe</span><a href="#" class="b-btn">Blame</a><a href="#" class="b-btn b-hide-m">Cronologia</a><a href="#" class="b-btn">Modifica</a></div>
  <div class="b-codebox">{code_table()}</div>
</section>"""
    fl = "".join(f'<a href="#" class="{"on" if on else ""}"><span>{f}</span><b class="p">{p}</b><b class="m">{m}</b></a>' for f, p, m, on in PR_FILES)
    diff = f"""<section data-s="diff" class="sec-pr"><div class="b-cols"><div>
  <h1 class="b-prt">Cache calda: sfratto LRU dei pack freddi <span>#412</span></h1>
  <div class="b-meta"><span class="b-pill">{ic("pr")} Aperta</span><span>riccardo unisce</span><span class="mono">feat/lru-evict</span><span>in</span><span class="mono">main</span></div>
  <div class="b-file"><div class="b-fh">{ic("file")}<span style="flex:1">internal/storage/tier/cache.go</span><span class="p">+41</span><span class="m">−12</span></div>{diff_table(COMMENT)}</div>
  <div class="b-file"><div class="b-fold">{ic("right")} <span class="mono">internal/storage/tier/cache_test.go</span><span class="b-grow"></span><span class="st-ok mono">+96</span><span class="st-err mono">−3</span></div></div>
</div>
<aside class="b-margin">
  <div><a href="#" class="b-btn pri" style="width:100%;justify-content:center">Invia revisione</a></div>
  <div><h4>File modificati</h4><div class="b-filelist">{fl}</div></div>
  <div class="sec-act"><h4>Controlli</h4>{checks_row()}</div>
  <div><h4>Revisori</h4><dl class="b-kv"><dt>marta.b</dt><dd class="st-warn">Commenti</dd><dt>gstefani</dt><dd class="faint">In attesa</dd></dl></div>
</aside></div></section>"""
    jobs = "".join(f'<span class="b-job {"on" if s=="run" else ""}">{job_icon(s)}{n}<span class="faint">{d}</span></span>' for s, n, d in JOBS)
    act = f"""<section data-s="actions" class="sec-act">
  <h1 class="b-prt">storage: sfratta i pack freddi oltre il budget</h1>
  <div class="b-meta"><span class="b-pill">{ic("redo","i spin")} In corso</span><span><span class="mono">ci.yml</span> run #1287, push di riccardo, 3 minuti fa</span><span class="b-grow"></span><a href="#" class="b-btn">Annulla run</a></div>
  <div class="b-jobs">{jobs}</div>
  <div class="b-logbox"><div class="b-logh">{ic("term")} test (linux/arm64)<span class="faint" style="font-weight:600">runner hz-arm-02</span></div>{log_html()}</div>
</section>"""
    tags = "".join(f'<div class="b-tag"><b>{t}</b>{"<span class=lt>"+l+"</span>" if l else "<span></span>"}<span class="mono faint sz">{d}</span><span class="faint">{w}</span></div>' for t, l, d, s, w in TAGS)
    stack = "".join(f'<i style="--w:{p}" title="{c}"></i>' for c, s, p in LAYERS)
    lay = "".join(f'<code>{c}</code><span class="dim">{s}</span>' for c, s, p in LAYERS)
    pkg = f"""<section data-s="pacchetto" class="sec-pkg"><div class="b-cols"><div>
  <h1 class="b-prt">ervisiod</h1>
  <div class="b-meta"><span class="b-pill">{ic("pkg")} Immagine OCI</span><span>pubblicata da ci.yml 2 ore fa</span></div>
  <div class="b-pull"><span>docker pull git.ervisio.app/fonlogen/ervisiod:0.5.2</span>{ic("copy")}</div>
  <div class="b-strip b-gap"><h2>Tag</h2></div>{tags}
  <div class="b-strip b-gap"><h2>Layer</h2><span class="dim">linux/amd64, 75,3 MB</span></div>
  <div class="b-stack">{stack}</div><div class="b-lay">{lay}</div>
</div>
<aside class="b-margin">
  <div><h4>Dettagli</h4><dl class="b-kv"><dt>Digest</dt><dd class="mono">9f2c1e7a</dd><dt>Piattaforme</dt><dd>amd64, arm64</dd><dt>Download</dt><dd>2.318</dd><dt>Firma</dt><dd class="st-ok">cosign</dd></dl></div>
  <div><h4>Storage</h4><p>Blob su Hetzner Object Storage. I 3 tag più recenti restano in cache calda.</p></div>
</aside></div></section>"""
    return css + f'<div class="b">{bar}{mast}{home}{file}{diff}{act}{pkg}</div>'


# ---------------------------------------------------------------- C: Corsie
def variant_c():
    css = """
<style>
.c { --hue: var(--sec-code); --hue-s: var(--sec-code-s); padding: 12px; min-height: 100vh; }
.c > section { display: none; }
.c-band { background: var(--hue-s); border-radius: 20px; padding: 14px 18px 18px; margin-bottom: 12px; }
.c-top { display: flex; align-items: center; gap: 12px; font-size: 13.5px; }
.c-top .rp { font-weight: 700; display: flex; gap: 6px; align-items: center; }
.c-top .rp span { color: var(--ink2); font-weight: 600; }
.c-top .lg { width: 28px; height: 28px; border-radius: 9px; background: var(--ink); color: #000; display: grid; place-items: center; font: 800 14px var(--font); }
.c-seg { display: flex; gap: 2px; margin-left: auto; background: rgba(0,0,0,.45); border-radius: 14px; padding: 4px; }
:root[data-theme=light] .c-seg { background: rgba(255,255,255,.7); }
.c-seg a { height: 32px; padding: 0 12px; border-radius: 10px; display: inline-flex; gap: 7px; align-items: center; color: var(--ink2); font-weight: 700; font-size: 13px; }
.c-seg a .i { color: var(--hue); }
.c-seg a[aria-current] { background: var(--hue); color: #000; }
.c-seg a[aria-current] .i { color: #000; }
.c-seg a em { font-style: normal; color: var(--ink3); font-size: 12px; }
.c-seg a[aria-current] em { color: rgba(0,0,0,.6); }
.c-title { display: flex; align-items: flex-end; gap: 16px; margin-top: 26px; flex-wrap: wrap; }
.c-sec { color: var(--hue); font-weight: 800; font-size: 14px; display: flex; gap: 7px; align-items: center; margin-bottom: 4px; }
.c-title h1 { margin: 0; font-size: 30px; font-weight: 800; letter-spacing: -.025em; line-height: 1.1; }
.c-title h1 span { color: var(--ink3); font-weight: 600; }
.c-title .meta { color: var(--ink2); font-size: 13.5px; margin-top: 6px; display: flex; gap: 10px; flex-wrap: wrap; align-items: center; }
.c-title .meta .mono { color: var(--ink); }
.c-grow { flex: 1; }
.c-btn { display: inline-flex; align-items: center; gap: 7px; height: 38px; padding: 0 15px; border-radius: 12px; background: rgba(0,0,0,.5); font-weight: 700; font-size: 13.5px; }
:root[data-theme=light] .c-btn { background: rgba(255,255,255,.8); }
.c-btn.pri { background: var(--hue); color: #000; }
.c-body { display: grid; grid-template-columns: 300px minmax(0, 1fr); gap: 12px; align-items: start; }
.c-lane { background: var(--surface); border-radius: 18px; padding: 8px; position: sticky; top: 12px; }
.c-lane h3 { margin: 4px 8px 8px; font-size: 13.5px; font-weight: 800; display: flex; gap: 8px; align-items: center; }
.c-lane h3 span { margin-left: auto; color: var(--ink3); font-weight: 600; font-size: 12.5px; }
.c-pane { background: var(--surface); border-radius: 18px; padding: 8px; min-width: 0; }
.c-pane + .c-pane { margin-top: 12px; }
.c-ph { display: flex; gap: 10px; align-items: center; padding: 6px 8px 10px; font-weight: 800; font-size: 14px; flex-wrap: wrap; }
.c-ph .i { color: var(--hue); }
.c-ph small { color: var(--ink3); font-weight: 600; font-size: 12.5px; }
.c-row { display: grid; grid-template-columns: 18px minmax(120px, 200px) 1fr auto; gap: 12px; align-items: center; padding: 8px 10px; border-radius: 12px; font-size: 13.5px; }
.c-row:hover { background: var(--sunk); }
.c-row.k-folder .i { color: var(--hue); } .c-row.k-file .i { color: var(--ink3); }
.c-row .nm { font-weight: 700; } .c-row .msg { color: var(--ink2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; } .c-row .tm { color: var(--ink3); font-size: 12.5px; white-space: nowrap; }
.c-tl { position: relative; padding: 2px 8px 6px 30px; display: flex; flex-direction: column; gap: 14px; font-size: 13px; }
.c-tl::before { content: ""; position: absolute; left: 15px; top: 8px; bottom: 10px; width: 2px; border-radius: 2px; background: var(--hue-s); }
.c-ev { position: relative; }
.c-ev::before { content: ""; position: absolute; left: -20px; top: 4px; width: 10px; height: 10px; border-radius: 50%; background: var(--surface); box-shadow: inset 0 0 0 2px var(--evc, var(--hue)); }
.c-ev b { display: block; font-weight: 700; line-height: 1.35; }
.c-ev span { color: var(--ink3); font-size: 12.5px; }
.c-md { padding: 4px 14px 12px; font-size: 14.5px; line-height: 1.6; max-width: 72ch; }
.c-md h1 { font-size: 22px; font-weight: 800; margin: 6px 0 8px; } .c-md h2 { font-size: 16px; font-weight: 800; margin: 18px 0 6px; }
.c-md code { font: 12.5px var(--mono); background: var(--sunk); padding: 1px 5px; border-radius: 6px; }
.c-md pre { background: var(--bg); border-radius: 12px; padding: 12px 14px; overflow-x: auto; } .c-md pre code { background: none; }
.c-tree { display: flex; flex-direction: column; gap: 1px; font-size: 13px; }
.c-tree a { display: flex; gap: 8px; align-items: center; padding: 6px 8px; border-radius: 10px; color: var(--ink2); }
.c-tree a:hover { background: var(--sunk); }
.c-tree a.on { background: var(--hue-s); color: var(--hue); font-weight: 700; }
.c-tree a .i { width: 15px; height: 15px; }
.c-tree .d1 { padding-left: 24px; } .c-tree .d2 { padding-left: 40px; } .c-tree .d3 { padding-left: 56px; }
.c-code { background: var(--bg); border-radius: 12px; padding: 10px 0; }
.c-fs a { display: flex; gap: 8px; align-items: center; padding: 8px; border-radius: 10px; font: 12px var(--mono); color: var(--ink2); }
.c-fs a span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.c-fs a.on { background: var(--hue-s); color: var(--hue); }
.c-fs .p { color: var(--ok); } .c-fs .m { color: var(--err); }
.c-fs .rv { display: inline-block; width: 8px; height: 8px; border-radius: 50%; background: var(--ok); flex: none; }
.c-fs .rv.no { background: var(--sunk); box-shadow: inset 0 0 0 2px var(--ink3); }
.c-sticky { position: sticky; top: 0; z-index: 2; display: flex; gap: 10px; align-items: center; background: var(--surface); padding: 6px 8px 10px; font: 600 12.5px var(--mono); }
.c-sticky .p { color: var(--ok); } .c-sticky .m { color: var(--err); }
.diff tr.cmt td { padding: 10px 14px; background: var(--hue-s); white-space: normal; font-family: var(--font); }
.thread { display: flex; flex-direction: column; gap: 12px; font-size: 13.5px; max-width: 700px; }
.tc { display: flex; gap: 10px; } .tc p { margin: 3px 0 0; line-height: 1.5; }
.tc code { font: 12px var(--mono); background: rgba(0,0,0,.4); padding: 1px 5px; border-radius: 6px; }
.treply { background: rgba(0,0,0,.4); border-radius: 12px; padding: 9px 12px; color: var(--ink3); }
.c-graph { display: flex; align-items: center; gap: 0; padding: 6px 8px 14px; overflow-x: auto; }
.c-node { flex: none; display: flex; flex-direction: column; gap: 6px; }
.c-job { display: flex; gap: 8px; align-items: center; height: 40px; padding: 0 12px; border-radius: 12px; background: var(--sunk); font-weight: 700; font-size: 13px; white-space: nowrap; }
.c-job.on { box-shadow: inset 0 0 0 2px var(--hue); }
.c-job .faint { font-weight: 600; font-size: 12px; }
.c-wire { flex: none; width: 28px; height: 2px; background: var(--ink3); opacity: .5; border-radius: 2px; }
.c-wire.done { background: var(--ok); opacity: 1; }
.c-log { background: var(--bg); border-radius: 12px; padding: 10px 0; }
.c-steps { display: flex; flex-direction: column; gap: 2px; }
.c-step { display: flex; gap: 10px; align-items: center; padding: 8px; border-radius: 10px; font-size: 13px; }
.c-step span:nth-child(2) { flex: 1; } .c-step.on { background: var(--hue-s); color: var(--hue); font-weight: 700; }
.c-tags a { display: grid; grid-template-columns: 1fr auto; gap: 2px 8px; padding: 9px 10px; border-radius: 12px; }
.c-tags a:hover { background: var(--sunk); }
.c-tags a.on { background: var(--hue-s); }
.c-tags b { font: 600 13px var(--mono); } .c-tags a.on b { color: var(--hue); }
.c-tags .lt { font-size: 11.5px; font-weight: 800; color: #000; background: var(--hue); padding: 1px 8px; border-radius: 999px; justify-self: end; }
.c-tags span { color: var(--ink3); font-size: 12.5px; }
.c-pull { display: flex; gap: 10px; align-items: center; background: var(--bg); border-radius: 12px; padding: 12px 14px; font: 13.5px var(--mono); margin: 0 0 4px; }
.c-pull span { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.c-layers { display: flex; flex-direction: column; gap: 6px; padding: 0 8px 8px; }
.c-ly { display: grid; grid-template-columns: 1fr 70px; align-items: center; gap: 12px; font-size: 13px; }
.c-ly .bar { height: 32px; border-radius: 10px; background: var(--sunk); position: relative; overflow: hidden; display: flex; align-items: center; padding: 0 12px; }
.c-ly .bar::before { content: ""; position: absolute; inset: 0 auto 0 0; width: var(--w); background: var(--hue-s); }
.c-ly code { position: relative; font: 12px var(--mono); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.c-ly span { text-align: right; color: var(--ink2); }
.c-plat { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; padding: 0 8px 8px; }
.c-plat div { background: var(--sunk); border-radius: 12px; padding: 12px; font-size: 13px; }
.c-plat b { display: block; font: 600 13px var(--mono); margin-bottom: 2px; }
@media (max-width: 900px) {
  .c { padding: 8px; }
  .c-band { padding: 12px 12px 16px; }
  .c-top { flex-wrap: wrap; }
  .c-seg { margin-left: 0; width: 100%; overflow-x: auto; scrollbar-width: none; order: 3; }
  .c-seg a { flex: none; }
  .c-title { margin-top: 18px; } .c-title h1 { font-size: 24px; }
  .c-body { grid-template-columns: minmax(0,1fr); }
  .c-body > * { min-width: 0; }
  .c-lane { position: static; order: 2; }
  .c-row { grid-template-columns: 18px 1fr auto; } .c-row .msg { display: none; }
  .c-hide-m { display: none !important; }
}
</style>"""
    seg = nav_items("", None)
    def band(section_label, icon, title, meta, actions):
        return f"""<div class="c-band"><div class="c-top"><span class="lg">E</span><span class="rp"><span>fonlogen /</span> ervisio-core</span><nav class="c-seg">{seg}</nav></div>
<div class="c-title"><div><div class="c-sec">{ic(icon)} {section_label}</div><h1>{title}</h1><div class="meta">{meta}</div></div><span class="c-grow"></span>{actions}</div></div>"""

    tl = "".join(f'<div class="c-ev" style="--evc:{c}"><b>{t}</b><span>{m}</span></div>' for t, m, c in [
        ("storage: sfratta i pack freddi oltre il budget", "riccardo, 2 ore fa", "var(--h-file)"),
        ("ci.yml #1287 in corso", "3 di 5 job", "var(--h-term)"),
        ("PR #412 aperta: sfratto LRU dei pack freddi", "riccardo, ieri", "var(--h-svc)"),
        ("ervisiod 0.5.2 pubblicato", "registry, 2 ore fa", "var(--h-sw)"),
        ("Issue #388 chiusa dal commit a3f9c21", "marta.b, ieri", "var(--h-usr)"),
    ])
    home = f"""<section data-s="home" class="sec-code">
{band("Codice", "code", "ervisio-core", '<span>' + ic("lock") + ' Privato</span><span>312 commit</span><span>14 branch</span><span class="c-hide-m">412 MB in cache calda, copia su Hetzner Object Storage</span>', f'<a href="#" class="c-btn">{ic("branch")} main</a><a href="#" class="c-btn pri">{ic("copy")} Clona</a>')}
<div class="c-body">
  <aside class="c-lane"><h3>Attività <span>oggi</span></h3><div class="c-tl">{tl}</div></aside>
  <div><div class="c-pane">{file_rows("c-row","nm","msg","tm")}</div>
  <div class="c-pane"><div class="c-ph">{ic("book")} README.md</div><div class="c-md">{README}</div></div></div>
</div></section>"""
    tree = f"""<div class="c-tree">
<a href="#">{ic("folder")} .github</a><a href="#">{ic("folder")} cmd</a><a href="#">{ic("folder")} internal</a>
<a href="#" class="d1">{ic("folder")} daemon</a><a href="#" class="d1">{ic("folder")} storage</a>
<a href="#" class="d2">{ic("folder")} sftp</a><a href="#" class="d2">{ic("folder")} tier</a>
<a href="#" class="d3 on">{ic("file")} cache.go</a><a href="#" class="d3">{ic("file")} cache_test.go</a><a href="#" class="d3">{ic("file")} lru.go</a>
<a href="#">{ic("folder")} web</a><a href="#">{ic("file")} README.md</a></div>"""
    file = f"""<section data-s="file" class="sec-code">
{band("Codice", "code", 'cache.go', '<span class="mono">internal/storage/tier/cache.go</span><span>142 righe</span><span class="c-hide-m">riccardo, 2 ore fa</span>', f'<a href="#" class="c-btn">Blame</a><a href="#" class="c-btn">Modifica</a>')}
<div class="c-body"><aside class="c-lane c-hide-m"><h3>File <span>main</span></h3>{tree}</aside>
<div class="c-pane"><div class="c-code">{code_table()}</div></div></div></section>"""
    fs = "".join(f'<a href="#" class="{"on" if on else ""}"><i class="rv {"" if i < 2 else "no"}"></i><span>{f}</span><b class="p">{p}</b><b class="m">{m}</b></a>' for i, (f, p, m, on) in enumerate(PR_FILES))
    diff = f"""<section data-s="diff" class="sec-pr">
{band("Pull request", "pr", 'Cache calda: sfratto LRU dei pack freddi <span>#412</span>', '<span>Aperta</span><span class="mono">feat/lru-evict</span><span>in</span><span class="mono">main</span><span class="c-hide-m">3 controlli su 5 passati</span>', '<a href="#" class="c-btn pri">Invia revisione</a>')}
<div class="c-body"><aside class="c-lane"><h3>File <span>2 di 5 visti</span></h3><div class="c-fs">{fs}</div></aside>
<div class="c-pane" style="padding-top:0"><div class="c-sticky">{ic("file")}<span style="flex:1">internal/storage/tier/cache.go</span><span class="p">+41</span><span class="m">−12</span></div>{diff_table(COMMENT)}</div></div></section>"""
    nodes = []
    for idx, (s, n, d) in enumerate(JOBS):
        on = " on" if s == "run" else ""
        nodes.append(f'<div class="c-node"><div class="c-job{on}">{job_icon(s)}{n}<span class="faint">{d}</span></div></div>')
    # lint -> [test amd64, test arm64] -> build -> push
    graph = (f'{nodes[0]}<div class="c-wire done"></div><div class="c-node">'
             f'{nodes[1][len("<div class=\"c-node\">"):-6]}{nodes[2][len("<div class=\"c-node\">"):-6]}</div>'
             f'<div class="c-wire"></div>{nodes[3]}<div class="c-wire"></div>{nodes[4]}')
    steps = "".join(f'<div class="c-step{" on" if on else ""}">{job_icon(s)}<span>{n}</span><span class="faint">{d}</span></div>' for s, n, d, on in [
        ("ok", "Set up job", "2 s", False), ("ok", "actions/checkout@v4", "4 s", False), ("ok", "actions/setup-go@v5", "11 s", False),
        ("run", "go test -race ./internal/storage/...", "2 min 48 s", True), ("wait", "upload-artifact coverage", "", False)])
    act = f"""<section data-s="actions" class="sec-act">
{band("Actions", "play", 'storage: sfratta i pack freddi oltre il budget', '<span class="st-run">' + ic("redo","i spin") + ' In corso</span><span class="mono">ci.yml</span><span>run #1287</span><span class="c-hide-m">push su main di riccardo, 3 minuti fa</span>', '<a href="#" class="c-btn">Annulla</a>')}
<div class="c-pane"><div class="c-graph">{graph}</div></div>
<div class="c-body" style="margin-top:12px"><aside class="c-lane"><h3>test (linux/arm64) <span>hz-arm-02</span></h3><div class="c-steps">{steps}</div></aside>
<div class="c-pane"><div class="c-ph">{ic("term")} go test -race ./internal/storage/... <small>Firecracker, 4 vCPU</small></div><div class="c-log">{log_html()}</div></div></div></section>"""
    tags = "".join(f'<a href="#" class="{"on" if i==0 else ""}"><b>{t}</b>{"<em class=lt>"+l+"</em>" if l else "<span></span>"}<span>{w}</span><span class="mono">{d}</span></a>' for i, (t, l, d, s, w) in enumerate(TAGS))
    lay = "".join(f'<div class="c-ly"><div class="bar" style="--w:{p*2.6}%"><code>{c}</code></div><span>{s}</span></div>' for c, s, p in LAYERS)
    pkg = f"""<section data-s="pacchetto" class="sec-pkg">
{band("Pacchetti", "pkg", 'ervisiod', '<span>Immagine OCI</span><span>2.318 download</span><span>firmata con cosign</span>', '<a href="#" class="c-btn">Impostazioni</a>')}
<div class="c-body"><aside class="c-lane"><h3>Tag <span>4</span></h3><div class="c-tags">{tags}</div></aside>
<div><div class="c-pane"><div class="c-ph">Scarica 0.5.2</div><div class="c-pull"><span>docker pull git.ervisio.app/fonlogen/ervisiod:0.5.2</span>{ic("copy")}</div></div>
<div class="c-pane"><div class="c-ph">Piattaforme</div><div class="c-plat"><div><b>linux/amd64</b><span class="dim">75,3 MB, 4 layer</span></div><div><b>linux/arm64</b><span class="dim">72,9 MB, 4 layer</span></div></div></div>
<div class="c-pane"><div class="c-ph">{ic("layers")} Layer di linux/amd64 <small>i blob stanno su Hetzner Object Storage</small></div><div class="c-layers">{lay}</div></div></div></div></section>"""
    return css + f'<div class="c">{home}{file}{diff}{act}{pkg}</div>'


def main():
    os.makedirs(ROUND, exist_ok=True)
    for vid, fn in (("a", variant_a), ("b", variant_b), ("c", variant_c)):
        html = fn()
        for d in (ROUND, HERE):
            name = f"{vid}.html" if d == ROUND else f"direzione-{vid}.html"
            with open(os.path.join(d, name), "w") as f:
                f.write(html)
    print("ok")

if __name__ == "__main__":
    main()
