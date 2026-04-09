#!/usr/bin/env python3
"""
One-time migration: flatten Jinja2 templates to static HTML for the Go backend.

Renders each Python/Jinja2 page template with a stub context (no credentials
loaded, no current change) so server-injected state defaults to the correct
"not authenticated / nothing loaded" state. All dynamic data will be fetched
by JS on DOMContentLoaded once the Go backend serves these pages.

Usage:
    python3 scripts/migrate-frontend.py

Output:
    web/index.html
    web/aws/auth.html
    web/aws/script-runner.html
    ... (one file per page)
"""

import json
import re
import sys
from pathlib import Path

try:
    from jinja2 import Environment, FileSystemLoader, Undefined, pass_context
except ImportError:
    print("ERROR: jinja2 not installed. Run: pip install jinja2")
    sys.exit(1)

# ── Paths ─────────────────────────────────────────────────────────────────────

TEMPLATES_DIR = Path("/home/todd/git/CloudOpsTools/backend/templates")
OUTPUT_DIR    = Path("/home/todd/git/GOrg-CloudTools/web")

# ── Jinja2 stub globals ───────────────────────────────────────────────────────

class SilentUndefined(Undefined):
    """All undefined variables silently render as empty string / falsy."""
    def _fail_with_undefined_error(self, *args, **kwargs):
        return ""
    __str__     = lambda self: ""
    __iter__    = lambda self: iter([])
    __len__     = lambda self: 0
    __bool__    = lambda self: False
    __call__    = lambda self, *args, **kwargs: SilentUndefined()
    def __getattr__(self, name):
        return SilentUndefined()


def stub_url_for(endpoint, **kwargs):
    """Flask's url_for — only used for static assets in these templates."""
    if endpoint == "static":
        path = kwargs.get("path", "")
        # Ensure leading slash
        if not path.startswith("/"):
            path = "/" + path
        return f"/static{path}"
    return f"/{endpoint}"


def stub_get_flashed_messages(with_categories=False):
    return []


def tojson_filter(value, **kwargs):
    return json.dumps(value)


def selectattr_filter(iterable, attr, *args):
    """Minimal Jinja2 selectattr — used for counting linux/windows instances."""
    if not iterable:
        return []
    op  = args[0] if args else "equalto"
    val = args[1] if len(args) > 1 else None
    if op == "equalto":
        return [item for item in iterable if getattr(item, attr, None) == val]
    return list(iterable)


# ── Jinja2 environment ────────────────────────────────────────────────────────

env = Environment(
    loader=FileSystemLoader(str(TEMPLATES_DIR)),
    undefined=SilentUndefined,
    autoescape=False,
    trim_blocks=True,
    lstrip_blocks=True,
)
env.globals["url_for"]              = stub_url_for
env.globals["get_flashed_messages"] = stub_get_flashed_messages
env.globals["request"]              = type("Request", (), {
    "path": "",
    "session": {},
    "url": "",
})()
env.filters["tojson"]               = tojson_filter
env.filters["selectattr"]           = selectattr_filter

# ── Stub context (all server-injected state defaults to empty / unauthenticated)

BASE_CONTEXT = {
    "gov_credentials": False,
    "com_credentials": False,
    "current_change":  None,
    "changes":         [],
    "tools":           [],
    "scripts":         [],
    "tool_name":       "",
    "tool_endpoint":   "/aws/script-runner",
    "selector_id":     "instance-selector",
    "selector_title":  "Instance Selection",
}

# ── Pages to render ───────────────────────────────────────────────────────────
# (template path relative to TEMPLATES_DIR, output path relative to OUTPUT_DIR,
#  any page-specific context overrides)

PAGES = [
    ("index.html",                      "index.html",               {}),
    ("aws/auth.html",                   "aws/auth.html",            {}),
    ("aws/tools.html",                  "aws/tools.html",           {}),
    ("aws/script_runner.html",          "aws/script-runner.html",   {
        "tool_name":     "script-runner",
        "tool_endpoint": "/aws/script-runner",
        "selector_id":   "instance-selector",
    }),
    ("aws/linux_qc_patching_prep.html", "aws/linux-qc-prep.html",  {
        "tool_name":     "linux-qc-prep",
        "tool_endpoint": "/aws/linux-qc-prep",
        "selector_id":   "instance-selector",
    }),
    ("aws/linux_qc_patching_post.html", "aws/linux-qc-post.html",  {
        "tool_name":     "linux-qc-post",
        "tool_endpoint": "/aws/linux-qc-post",
        "selector_id":   "instance-selector",
    }),
    ("aws/sft_fixer.html",              "aws/sft-fixer.html",       {
        "tool_name":     "sft-fixer",
        "tool_endpoint": "/aws/sft-fixer",
    }),
    ("aws/vpc_recon.html",              "aws/vpc-recon.html",       {
        "tool_name":     "vpc-recon",
        "tool_endpoint": "/aws/vpc-recon",
    }),
    ("aws/disk_recon.html",             "aws/disk-recon.html",      {
        "tool_name":     "disk-recon",
        "tool_endpoint": "/aws/disk-recon",
    }),
    ("aws/rhsa_compliance.html",        "aws/rhsa-compliance.html", {
        "tool_name":     "rhsa-compliance",
        "tool_endpoint": "/aws/rhsa-compliance",
    }),
    ("aws/decom_survey.html",           "aws/decom-survey.html",    {
        "tool_name":     "decom-survey",
        "tool_endpoint": "/aws/decom-survey",
    }),
    ("errors/404.html",                 "errors/404.html",          {}),
    ("errors/500.html",                 "errors/500.html",          {}),
]

# ── Post-processing ───────────────────────────────────────────────────────────

def post_process(html: str) -> str:
    """Clean up artefacts left after Jinja2 rendering."""

    # Remove the Stagewise third-party toolbar snippet — dev tool, not needed
    html = re.sub(
        r"<!-- Stagewise Toolbar Integration -->.*?</script>",
        "",
        html,
        flags=re.DOTALL,
    )

    # Collapse runs of 3+ blank lines to a single blank line
    html = re.sub(r"\n{3,}", "\n\n", html)

    return html.strip() + "\n"


# ── Main ──────────────────────────────────────────────────────────────────────

def main():
    errors = []
    for template_path, output_rel, overrides in PAGES:
        context = {**BASE_CONTEXT, **overrides}
        out_path = OUTPUT_DIR / output_rel
        out_path.parent.mkdir(parents=True, exist_ok=True)

        try:
            template = env.get_template(template_path)
            rendered = template.render(**context)
            cleaned  = post_process(rendered)
            out_path.write_text(cleaned, encoding="utf-8")
            print(f"  OK  {template_path:45s} → web/{output_rel}")
        except Exception as exc:
            print(f"  ERR {template_path:45s}   {exc}")
            errors.append((template_path, exc))

    print()
    if errors:
        print(f"Completed with {len(errors)} error(s).")
        sys.exit(1)
    else:
        print(f"All {len(PAGES)} pages rendered successfully → {OUTPUT_DIR}")


if __name__ == "__main__":
    main()
