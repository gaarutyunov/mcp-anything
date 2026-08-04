#!/usr/bin/env python3
"""Assert a built site actually works when it is served from a subdirectory.

A site built for the domain root but served from /pr-preview/pr-<N>/ fails
*silently*: the HTML renders fine, every stylesheet, script and link resolves
against the domain root instead, and the workflow that published it stays
green. The page looks broken to a human and looks perfect to CI. This checker
is what turns that into a build failure.

It serves a staged directory over real HTTP and, for every page it is given:

  1. the page itself must return 200;
  2. every root-absolute URL on it must start with the preview prefix -- a
     bare "/docs/routing" escapes the preview and silently shows the
     production site instead;
  3. every one of those URLs must itself return 200;
  4. at least one .css or .js asset must have been checked, so that a page
     which happens to reference nothing cannot vacuously pass.

Usage:
    check-preview.py --root DIR --prefix /pr-preview/pr-1 --page /pr-preview/pr-1/ [--page ...]
"""

from __future__ import annotations

import argparse
import functools
import http.server
import socketserver
import sys
import threading
import urllib.error
import urllib.request
from html.parser import HTMLParser

# Attributes this site puts URLs in. `content` covers the og:/twitter: meta
# tags, which carry absolute production URLs and are deliberately not rebased.
URL_ATTRS = ("href", "src")


class URLCollector(HTMLParser):
    """Collect every href/src on a page, in document order."""

    def __init__(self) -> None:
        super().__init__()
        self.urls: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        for name, value in attrs:
            if name in URL_ATTRS and value:
                self.urls.append(value)


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, fmt: str, *args: object) -> None:  # noqa: A003
        pass


def serve(root: str) -> tuple[socketserver.TCPServer, int]:
    handler = functools.partial(QuietHandler, directory=root)
    httpd = socketserver.TCPServer(("127.0.0.1", 0), handler)
    port = httpd.server_address[1]
    threading.Thread(target=httpd.serve_forever, daemon=True).start()
    return httpd, port


def fetch(base_url: str, path: str) -> tuple[int, str]:
    """GET a path; return (status, body). Never raises for an HTTP error."""
    try:
        with urllib.request.urlopen(base_url + path, timeout=30) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        return exc.code, ""
    except OSError as exc:  # connection refused, timeout, ...
        print(f"    request failed: {exc}", file=sys.stderr)
        return 0, ""


def is_local_absolute(url: str) -> bool:
    """Root-absolute, same-origin URLs only: '/x' yes, '//cdn/x' and 'https://' no."""
    return url.startswith("/") and not url.startswith("//")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True, help="directory to serve")
    parser.add_argument("--prefix", required=True, help="preview prefix, e.g. /pr-preview/pr-1")
    parser.add_argument("--page", action="append", required=True, help="page path to check (repeatable)")
    args = parser.parse_args()

    prefix = "/" + args.prefix.strip("/")
    httpd, port = serve(args.root)
    base_url = f"http://127.0.0.1:{port}"
    failures: list[str] = []
    assets_checked = 0

    try:
        for page in args.page:
            status, body = fetch(base_url, page)
            print(f"  {status}  {page}")
            if status != 200:
                failures.append(f"page {page} returned {status}, expected 200")
                continue

            collector = URLCollector()
            collector.feed(body)

            seen: set[str] = set()
            for url in collector.urls:
                url = url.split("#", 1)[0]
                if not url or url in seen or not is_local_absolute(url):
                    continue
                seen.add(url)

                # The whole point: a root-absolute URL that is not under the
                # preview prefix resolves against the production site.
                if not (url == prefix or url.startswith(prefix + "/")):
                    failures.append(
                        f"{page}: '{url}' is not under '{prefix}' -- it would "
                        f"resolve against the production site, not this preview"
                    )
                    continue

                asset_status, _ = fetch(base_url, url)
                print(f"    {asset_status}  {url}")
                if asset_status != 200:
                    failures.append(f"{page}: '{url}' returned {asset_status}, expected 200")
                if url.endswith((".css", ".js")):
                    assets_checked += 1
    finally:
        httpd.shutdown()
        httpd.server_close()

    if not assets_checked:
        failures.append(
            "no .css/.js asset was checked -- the pages referenced none, so this "
            "run proves nothing about asset paths"
        )

    if failures:
        print(f"\n{len(failures)} problem(s) serving from {prefix}:", file=sys.stderr)
        for failure in failures:
            print(f"  - {failure}", file=sys.stderr)
        return 1

    print(f"\nOK: every page and asset resolves under {prefix} ({assets_checked} asset(s) checked)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
