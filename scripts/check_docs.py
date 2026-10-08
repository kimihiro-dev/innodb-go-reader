#!/usr/bin/env python3
"""Check documentation structure and local links without modifying files."""

import re
import sys
import unicodedata
from collections import Counter
from pathlib import Path
from urllib.parse import unquote, urlsplit


ROOT = Path(__file__).resolve().parents[1]


def slug(text):
    text = re.sub(r"<[^>]*>", "", text).lower()
    return "".join(
        char for char in text
        if char in "-_" or not unicodedata.category(char).startswith(("P", "S"))
    ).replace(" ", "-")


def prose_lines(text):
    fence = None
    result = []
    for number, line in enumerate(text.splitlines(), 1):
        match = re.match(r"^\s*(`{3,}|~{3,})", line)
        if match:
            marker = match[1]
            if fence is None:
                fence = (marker[0], len(marker))
            elif marker[0] == fence[0] and len(marker) >= fence[1]:
                fence = None
            continue
        if fence is None:
            result.append((number, line))
    return result, fence


def anchors(text):
    lines, _ = prose_lines(text)
    result = set()
    seen = Counter()
    for _, line in lines:
        match = re.match(r"^#{1,6}\s+(.+)$", line)
        if match:
            base = slug(match[1])
            suffix = "" if seen[base] == 0 else f"-{seen[base]}"
            seen[base] += 1
            result.add(base + suffix)
        result.update(re.findall(r'<a\s+(?:id|name)="([^"]+)"', line))
    return result


def main():
    files = sorted((ROOT / "docs").rglob("*.md"))
    errors = []
    links = 0
    anchor_cache = {}
    for path in files:
        text = path.read_text(encoding="utf-8")
        all_lines = text.splitlines()
        lines, fence = prose_lines(text)
        label = str(path.relative_to(ROOT))

        def fail(number, message):
            errors.append(f"{label}:{number}: {message}")

        if not text.endswith("\n") or "\r" in text:
            fail(1, "use LF lines and a final newline")
        if fence is not None:
            fail(len(all_lines), "unclosed code fence")
        headings = [(n, line) for n, line in lines if re.match(r"^#{1,6} ", line)]
        if sum(line.startswith("# ") for _, line in headings) != 1:
            fail(1, "exactly one level-one title is required")
        previous = 0
        for number, line in headings:
            level = len(line) - len(line.lstrip("#"))
            if level > previous + 1:
                fail(number, "heading levels must be consecutive")
            previous = level
            if level > 1 and re.match(r"^#{2,6} (?:\d+[.)]\s*|[一二三四五六七八九十]+、)", line):
                fail(number, "subheadings must not mix numbered outlines")
            if number > 1 and all_lines[number - 2].strip():
                fail(number, "a blank line is required before headings")
            if number < len(all_lines) and all_lines[number].strip():
                fail(number, "a blank line is required after headings")
        if path.parent.name == "format" and re.match(r"^\d{2}-", path.name):
            if not all_lines[0].startswith(f"# {path.name[:2]}. "):
                fail(1, "chapter titles must use '# NN. Title'")
        explicit = re.findall(r'<a\s+(?:id|name)="([^"]+)"', text)
        if len(explicit) != len(set(explicit)):
            fail(1, "duplicate explicit anchors")
        prose_numbers = {number for number, _ in lines}
        for number, line in lines:
            previous_line = all_lines[number - 2] if number > 1 else ""
            next_line = all_lines[number] if number < len(all_lines) else ""
            if not line and not previous_line and number - 1 in prose_numbers:
                fail(number, "use a single blank line between blocks")
            if re.match(r"^(?:[-*+] |\d+[.)] )", line) and previous_line.strip():
                if not re.match(r"^(?:[-*+] |\d+[.)] |\s)", previous_line):
                    fail(number, "a blank line is required before lists")
            if line.startswith("|"):
                separator = bool(re.fullmatch(r"[| :\-]+", next_line))
                if not previous_line.startswith("|") and not separator:
                    fail(number, "table rows must stay together after a header/separator")
                if previous_line.strip() and not previous_line.startswith("|"):
                    fail(number, "a blank line is required before tables")
                if next_line.strip() and not next_line.startswith("|"):
                    fail(number, "a blank line is required after tables")
        active_fence = None
        for number, line in enumerate(all_lines, 1):
            marker = re.match(r"^\s*(`{3,}|~{3,})", line)
            if not marker:
                continue
            if active_fence is None:
                active_fence = (marker[1][0], len(marker[1]))
                if number > 1 and all_lines[number - 2].strip():
                    fail(number, "a blank line is required before code fences")
            elif marker[1][0] == active_fence[0] and len(marker[1]) >= active_fence[1]:
                active_fence = None
                if number < len(all_lines) and all_lines[number].strip():
                    fail(number, "a blank line is required after code fences")
        for number, line in lines:
            for reference in re.findall(r'!?\[[^\]]*\]\(([^)]+)\)', line):
                reference = reference.strip().strip("<>")
                parsed = urlsplit(reference)
                if parsed.scheme or parsed.netloc:
                    continue
                links += 1
                target = (path.parent / unquote(parsed.path)).resolve() if parsed.path else path
                if not target.is_file():
                    fail(number, f"missing local file: {reference}")
                    continue
                fragment = unquote(parsed.fragment)
                if not fragment:
                    continue
                if target.suffix == ".md":
                    if target not in anchor_cache:
                        anchor_cache[target] = anchors(target.read_text(encoding="utf-8"))
                    if fragment not in anchor_cache[target]:
                        fail(number, f"missing local anchor: {reference}")
                elif re.fullmatch(r"L\d+(?:-L\d+)?", fragment):
                    numbers = [int(value) for value in re.findall(r"\d+", fragment)]
                    count = len(target.read_text(encoding="utf-8").splitlines())
                    if any(value < 1 or value > count for value in numbers):
                        fail(number, f"source line outside file: {reference}")
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"Documentation OK: {len(files)} Markdown files, {links} local links/anchors")
    return 0


if __name__ == "__main__":
    sys.exit(main())
