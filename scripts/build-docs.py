#!/usr/bin/env python3
"""Builds the French documentation PDFs from docs/fr/*.md (needs: pip install markdown, and Microsoft Edge or Chrome).

    python scripts/build-docs.py            # writes docs/pdf/*.pdf

The Markdown files stay the source of truth; the screenshots live in docs/img.
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
SRC = ROOT / "docs" / "fr"
OUT = ROOT / "docs" / "pdf"
LOGO = ROOT / "brand" / "logo-white.svg"
DATE = "20 novembre 2026"

DOCS = [
    ("manuel-utilisateur", "Manuel d'utilisation", "Guide pas à pas pour les collaborateurs"),
    ("fonctionnalites", "Fonctionnalités", "Présentation pour la direction, la DSI et les équipes métier"),
    ("feuille-de-route", "Feuille de route", "Mise en production, évolutions et envoi de courriels externes"),
]

SLUGS = {"manuel-utilisateur": "Manuel-utilisateur", "fonctionnalites": "Fonctionnalites", "feuille-de-route": "Feuille-de-route"}

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
.toc { page-break-after: always; }
.toc h1 { page-break-before: avoid; }
.toc ol { list-style: none; padding: 0; margin: 4mm 0; }
.toc li { padding: 2mm 0; border-bottom: 1px dotted var(--line); font-size: 11.5pt; }
.toc li small { color: var(--muted); display: block; font-size: 9pt; }
"""


def render(md_text: str) -> str:
    body = markdown.markdown(md_text, extensions=["tables", "fenced_code", "sane_lists"])
    return body


def page(title: str, parts: list[tuple[str, str, str]], subtitle: str) -> str:
    logo = ""
    if LOGO.exists():
        logo = f'<img class="logo" src="{LOGO.as_uri()}" alt="Exim Bank">'
    cover = (
        f'<section class="cover">{logo}<h1>{html.escape(title)}</h1>'
        f'<div class="sub">{html.escape(subtitle)}</div><div class="line"></div>'
        f'<div class="sub" style="font-size:12pt">Plateforme de communication interne d\'Exim Bank</div>'
        f'<div class="meta">Version du {DATE}<br>Document interne</div></section>'
    )
    toc = ""
    if len(parts) > 1:
        items = "".join(f"<li>{html.escape(t)}<small>{html.escape(s)}</small></li>" for _, t, s in parts)
        toc = f'<section class="toc"><h1>Sommaire</h1><ol>{items}</ol></section>'
    bodies = []
    for i, (name, _t, _s) in enumerate(parts):
        text = (SRC / f"{name}.md").read_text(encoding="utf-8")
        h = render(text)
        if i == 0 and not toc:
            h = h.replace("<h1>", '<h1 class="first">', 1)
        bodies.append(h)
    return f'<!doctype html><html lang="fr"><head><meta charset="utf-8"><title>{html.escape(title)}</title><style>{CSS}</style></head><body>{cover}{toc}{"".join(bodies)}</body></html>'


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


def to_pdf(html_text: str, out: Path) -> None:
    # relative image paths (../img/x.png) resolve against docs/fr, so the temporary page lives there
    tmp = SRC / f"_build_{out.stem}.html"
    tmp.write_text(html_text, encoding="utf-8")
    try:
        profile = tempfile.mkdtemp(prefix="docs-pdf-")
        subprocess.run(
            [browser(), "--headless=new", "--disable-gpu", f"--user-data-dir={profile}", "--no-pdf-header-footer",
             f"--print-to-pdf={out}", tmp.as_uri()],
            check=True, capture_output=True, timeout=180,
        )
    finally:
        tmp.unlink(missing_ok=True)
    print("wrote", out.relative_to(ROOT), f"({out.stat().st_size // 1024} KB)")


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    keep = {"Exim-Connect-Documentation-complete.pdf", *(f"Exim-Connect-{v}.pdf" for v in SLUGS.values())}
    for old in OUT.glob("Exim-Connect-*.pdf"):  # leftovers of an earlier naming of this very script
        if old.name not in keep:
            old.unlink()
    to_pdf(page("Exim Connect : documentation complète", DOCS, "Fonctionnalités, manuel d'utilisation et feuille de route"), OUT / "Exim-Connect-Documentation-complete.pdf")
    for name, title, sub in DOCS:
        to_pdf(page(f"Exim Connect : {title}", [(name, title, sub)], sub), OUT / f"Exim-Connect-{SLUGS[name]}.pdf")


if __name__ == "__main__":
    main()
