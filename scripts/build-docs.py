#!/usr/bin/env python3
"""Builds the documentation PDFs (French and English) from docs/<lang>/*.md.
Needs: pip install markdown, and Microsoft Edge or Chrome (used to print the PDF).

    python scripts/build-docs.py            # both languages -> docs/pdf/fr and docs/pdf/en
    python scripts/build-docs.py en         # one language

The Markdown files stay the source of truth; the screenshots live in docs/img (French UI) and docs/img-en (English UI).
"""
import html
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

import markdown

ROOT = Path(__file__).resolve().parent.parent
LOGO = ROOT / "brand" / "logo-white.svg"
MERMAID_VERSION = "10.9.2"
MERMAID = ROOT / "scripts" / ".cache" / "mermaid.min.js"  # downloaded once, not versioned

LANGS = {
    "fr": {
        "html_lang": "fr",
        "date": "20 novembre 2026",
        "version": "Version du",
        "internal": "Document interne",
        "platform": "Plateforme de communication interne d'Exim Bank",
        "toc": "Sommaire",
        "all_title": "Exim Connect : documentation complète",
        "all_sub": "Fonctionnalités, manuel, guide technique et feuille de route",
        "all_file": "Exim-Connect-Documentation-complete",
        "docs": [
            ("manuel-utilisateur", "Manuel d'utilisation", "Guide pas à pas pour les collaborateurs", "Manuel-utilisateur"),
            ("fonctionnalites", "Fonctionnalités", "Présentation pour la direction, la DSI et les équipes métier", "Fonctionnalites"),
            ("guide-technique", "Guide technique", "Technologies, architecture, schémas, qualité et workflow Git", "Guide-technique"),
            ("feuille-de-route", "Feuille de route", "Mise en production, évolutions et envoi de courriels externes", "Feuille-de-route"),
        ],
    },
    "en": {
        "html_lang": "en",
        "date": "20 November 2026",
        "version": "Version of",
        "internal": "Internal document",
        "platform": "Exim Bank's internal communication platform",
        "toc": "Contents",
        "all_title": "Exim Connect: complete documentation",
        "all_sub": "Features, user manual, technical guide and roadmap",
        "all_file": "Exim-Connect-Complete-documentation",
        "docs": [
            ("user-manual", "User Manual", "Step-by-step guide for employees", "User-manual"),
            ("features", "Features", "Overview for management, IT and business teams", "Features"),
            ("technical-guide", "Technical Guide", "Technologies, architecture, diagrams, quality and Git workflow", "Technical-guide"),
            ("roadmap", "Roadmap", "Going to production, evolutions and sending external e-mail", "Roadmap"),
        ],
    },
}

CSS = """
@page { size: A4; margin: 18mm 16mm 20mm 16mm; }
:root { --navy:#002E6C; --navy2:#22317F; --gold:#A99564; --gold-light:#EFE9DB; --alert:#F21053; --ink:#1B2433; --muted:#5B6678; --line:#DDE2EA; }
* { box-sizing: border-box; }
body { font: 10.5pt/1.5 "Segoe UI", "Helvetica Neue", Arial, sans-serif; color: var(--ink); margin: 0; }
h1 { color: var(--navy); font-size: 22pt; margin: 0 0 4mm; border-bottom: 3px solid var(--gold); padding-bottom: 3mm; page-break-before: always; }
h1.first { page-break-before: avoid; }
h2 { color: var(--navy); font-size: 15pt; margin: 9mm 0 3mm; page-break-after: avoid; }
h3 { color: var(--navy2); font-size: 12pt; margin: 6mm 0 2mm; page-break-after: avoid; }
h4 { color: var(--navy2); font-size: 10.5pt; margin: 4mm 0 1mm; }
p, li { orphans: 3; widows: 3; }
em { color: var(--muted); }
a { color: var(--navy2); }
hr { border: 0; border-top: 1px solid var(--line); margin: 6mm 0; }
img { max-width: 100%; border: 1px solid var(--line); border-radius: 4px; display: block; margin: 3mm auto; page-break-inside: avoid; box-shadow: 0 1px 6px rgba(0,46,108,.12); }
table { border-collapse: collapse; width: 100%; margin: 3mm 0 5mm; font-size: 9.5pt; page-break-inside: auto; }
tr { page-break-inside: avoid; }
th { background: var(--navy); color: #fff; text-align: left; padding: 2mm 3mm; font-weight: 600; }
td { border-bottom: 1px solid var(--line); padding: 2mm 3mm; vertical-align: top; }
tr:nth-child(even) td { background: #F7F8FB; }
blockquote { margin: 4mm 0; padding: 3mm 5mm; background: var(--gold-light); border-left: 4px solid var(--gold); border-radius: 0 4px 4px 0; }
blockquote p { margin: 1mm 0; }
code { font-family: Consolas, "Courier New", monospace; font-size: 9.5pt; background: #EEF1F6; padding: 0 1.5mm; border-radius: 3px; }
pre { background: #0F1B33; color: #E6ECF7; padding: 4mm; border-radius: 6px; font-size: 8.3pt; line-height: 1.35; overflow: hidden; page-break-inside: avoid; }
pre code { background: none; padding: 0; color: inherit; font-size: inherit; }
.cover { height: 255mm; background: var(--navy); color: #fff; margin: -18mm -16mm 0; padding: 40mm 22mm; page-break-after: always; position: relative; }
.cover img.logo { background: none; border: 0; box-shadow: none; width: 70mm; margin: 0 0 30mm; }
.cover h1 { color: #fff; border: 0; font-size: 34pt; line-height: 1.15; page-break-before: avoid; margin: 0 0 6mm; }
.cover .sub { font-size: 15pt; color: #E6D9B5; margin-bottom: 4mm; }
.cover .line { width: 40mm; height: 4px; background: var(--gold); margin: 8mm 0; }
.cover .meta { position: absolute; left: 22mm; bottom: 22mm; font-size: 10.5pt; color: #C9D3E8; }
.mermaid { text-align: center; margin: 4mm 0 6mm; page-break-inside: avoid; }
.mermaid svg { max-width: 100%; height: auto; }
.toc { page-break-after: always; }
.toc h1 { page-break-before: avoid; }
.toc ol { list-style: none; padding: 0; margin: 4mm 0; }
.toc li { padding: 2mm 0; border-bottom: 1px dotted var(--line); font-size: 11.5pt; }
.toc li small { color: var(--muted); display: block; font-size: 9pt; }
"""


def mermaid_script() -> str:
    """Mermaid renders the ```mermaid blocks (the same ones GitHub draws); the library is fetched once and cached."""
    if not MERMAID.exists():
        import urllib.request
        MERMAID.parent.mkdir(parents=True, exist_ok=True)
        urllib.request.urlretrieve(f"https://cdn.jsdelivr.net/npm/mermaid@{MERMAID_VERSION}/dist/mermaid.min.js", MERMAID)
    theme = ("{startOnLoad:true,theme:'base',securityLevel:'loose',flowchart:{htmlLabels:true,curve:'basis'},"
             "themeVariables:{fontFamily:'Segoe UI, Arial, sans-serif',fontSize:'13px',primaryColor:'#EFE9DB',primaryBorderColor:'#002E6C',"
             "primaryTextColor:'#1B2433',lineColor:'#22317F',secondaryColor:'#DDE6F5',tertiaryColor:'#F7F8FB',noteBkgColor:'#FDF6E3',"
             "actorBkg:'#002E6C',actorTextColor:'#ffffff',actorBorder:'#002E6C',signalColor:'#22317F'}}")
    return f'<script src="{MERMAID.as_uri()}"></script><script>mermaid.initialize({theme});</script>'


def mermaid_blocks(h: str) -> str:
    def repl(m):
        return '<div class="mermaid">' + html.unescape(m.group(1)) + '</div>'
    return re.sub(r'<pre><code class="language-mermaid">(.*?)</code></pre>', repl, h, flags=re.S)


def page(cfg: dict, src: Path, title: str, parts: list, subtitle: str) -> str:
    logo = f'<img class="logo" src="{LOGO.as_uri()}" alt="Exim Bank">' if LOGO.exists() else ""
    cover = (
        f'<section class="cover">{logo}<h1>{html.escape(title)}</h1>'
        f'<div class="sub">{html.escape(subtitle)}</div><div class="line"></div>'
        f'<div class="sub" style="font-size:12pt">{html.escape(cfg["platform"])}</div>'
        f'<div class="meta">{cfg["version"]} {cfg["date"]}<br>{cfg["internal"]}</div></section>'
    )
    toc = ""
    if len(parts) > 1:
        items = "".join(f"<li>{html.escape(t)}<small>{html.escape(s)}</small></li>" for _, t, s, _f in parts)
        toc = f'<section class="toc"><h1>{cfg["toc"]}</h1><ol>{items}</ol></section>'
    bodies = []
    for i, (name, _t, _s, _f) in enumerate(parts):
        h = markdown.markdown((src / f"{name}.md").read_text(encoding="utf-8"), extensions=["tables", "fenced_code", "sane_lists"])
        h = mermaid_blocks(h)
        if i == 0 and not toc:
            h = h.replace("<h1>", '<h1 class="first">', 1)
        bodies.append(h)
    return (f'<!doctype html><html lang="{cfg["html_lang"]}"><head><meta charset="utf-8"><title>{html.escape(title)}</title>'
            f'<style>{CSS}</style></head><body>{cover}{toc}{"".join(bodies)}{mermaid_script()}</body></html>')


def browser() -> str:
    for c in (
        r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
        r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
        r"C:\Program Files\Google\Chrome\Application\chrome.exe",
        shutil.which("msedge") or "",
        shutil.which("google-chrome") or "",
        shutil.which("chromium") or "",
    ):
        if c and Path(c).exists():
            return c
    sys.exit("No Edge/Chrome found to print the PDF")


def to_pdf(src: Path, html_text: str, out: Path) -> None:
    # relative image paths (../img/x.png) resolve against docs/<lang>, so the temporary page lives there
    tmp = src / f"_build_{out.stem}.html"
    tmp.write_text(html_text, encoding="utf-8")
    try:
        profile = tempfile.mkdtemp(prefix="docs-pdf-")
        subprocess.run(
            [browser(), "--headless=new", "--disable-gpu", f"--user-data-dir={profile}", "--no-pdf-header-footer", "--virtual-time-budget=30000", "--allow-file-access-from-files",
             f"--print-to-pdf={out}", tmp.as_uri()],
            check=True, capture_output=True, timeout=180,
        )
    finally:
        tmp.unlink(missing_ok=True)
    print("wrote", out.relative_to(ROOT), f"({out.stat().st_size // 1024} KB)")


def build(lang: str) -> None:
    cfg = LANGS[lang]
    src = ROOT / "docs" / lang
    out = ROOT / "docs" / "pdf" / lang
    out.mkdir(parents=True, exist_ok=True)
    keep = {f"{cfg['all_file']}.pdf", *(f"Exim-Connect-{d[3]}.pdf" for d in cfg["docs"])}
    for old in out.glob("Exim-Connect-*.pdf"):  # leftovers of an earlier naming of this very script
        if old.name not in keep:
            old.unlink()
    to_pdf(src, page(cfg, src, cfg["all_title"], cfg["docs"], cfg["all_sub"]), out / f"{cfg['all_file']}.pdf")
    for d in cfg["docs"]:
        to_pdf(src, page(cfg, src, f"Exim Connect : {d[1]}" if lang == "fr" else f"Exim Connect: {d[1]}", [d], d[2]), out / f"Exim-Connect-{d[3]}.pdf")


if __name__ == "__main__":
    for lang in (sys.argv[1:] or list(LANGS)):
        build(lang)
