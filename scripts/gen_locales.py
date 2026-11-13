#!/usr/bin/env python3
"""Generate NSGo i18n catalogs from Pi Web's TypeScript message files.

Reads lib/i18n/messages/{en,zh-CN,zh-TW}.ts (which hold one flat object
literal of "key": "value" pairs) and writes native/locales/{en,zh-Hans,
zh-Hant}.yaml in the shape `nsgo i18n` expects, plus scripts/key_map.json
mapping every original key to the identifier NSGo generates for it.

Standard library only. Running it twice writes the same bytes.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SRC_DIR = ROOT / "lib" / "i18n" / "messages"
OUT_DIR = ROOT / "native" / "locales"
KEY_MAP = Path(__file__).resolve().parent / "key_map.json"

# The source language first: it carries every message, and only its
# messages may declare placeholders.
LANGS = [("en.ts", "en"), ("zh-CN.ts", "zh-Hans"), ("zh-TW.ts", "zh-Hant")]

# Arguments that are numbers. Everything else is a string: the messages
# that print costs, byte sizes and relative times receive them already
# formatted.
INT_ARGS = {
    "count", "total", "enabled", "tokens", "seconds", "minutes", "hours",
    "days", "index", "progress", "max", "median", "attempt", "retries",
    "opens", "requests", "unanswered", "errors", "failures", "additions",
    "deletions", "listeners", "detached", "exited", "alive", "closing",
    "starting", "shown",
}
DOUBLE_ARGS: set[str] = set()  # percentage and ratio arguments

ESCAPES = {"n": "\n", "t": "\t", "r": "\r", '"': '"', "\\": "\\", "/": "/", "'": "'"}


def parse_ts(path: Path) -> dict[str, str]:
    """parse_ts reads the messages object literal of one language's file."""
    text = path.read_text(encoding="utf-8")
    start = text.index("messages: {") + len("messages: {")
    end = text.rindex("},")
    body = text[start:end]

    def string_at(i: int) -> tuple[str, int]:
        if body[i] != '"':
            raise ValueError(f"{path}: {body[max(0, i - 40):i + 40]!r}: want a string")
        i += 1
        buf: list[str] = []
        while True:
            c = body[i]
            if c == "\\":
                e = body[i + 1]
                if e == "u":
                    buf.append(chr(int(body[i + 2:i + 6], 16)))
                    i += 6
                    continue
                buf.append(ESCAPES.get(e, e))
                i += 2
                continue
            if c == '"':
                return "".join(buf), i + 1
            buf.append(c)
            i += 1

    msgs: dict[str, str] = {}
    i, n = 0, len(body)
    while i < n:
        while i < n and body[i] in " \n\t\r,":
            i += 1
        if i >= n:
            break
        key, i = string_at(i)
        while body[i] in " \t\n\r":
            i += 1
        if body[i] != ":":
            raise ValueError(f"{path}: {key}: want ':'")
        i += 1
        while body[i] in " \t\n\r":
            i += 1
        value, i = string_at(i)
        if key in msgs:
            raise ValueError(f"{path}: {key} twice")
        msgs[key] = value
    return msgs


def ident_key(key: str) -> str:
    """ident_key turns "common.ok" into "commonOk": the words of every part,
    the first part lowercased, the others capitalized."""
    parts = [p for p in re.split(r"[^0-9A-Za-z]+", key) if p]
    if not parts:
        raise ValueError(f"{key!r} makes no identifier")
    out = [parts[0].lower()]
    for part in parts[1:]:
        out.append(part[0].upper() + part[1:])
    name = "".join(out)
    if not name[0].isalpha():
        raise ValueError(f"{key!r} makes {name}, which does not start with a letter")
    return name


ARG = re.compile(r"\{([A-Za-z_][A-Za-z0-9_]*)\}")


def segments(text: str, names: set[str]) -> list[tuple[bool, str]]:
    """segments splits text into (is_argument, text) runs.

    A name that is not a declared argument stays text, and so does one
    that a dollar sign introduces: Pi Web's messages write shell
    variables as ${name}, which is text in ICU MessageFormat.
    """
    out: list[tuple[bool, str]] = []
    i = 0
    lit: list[str] = []
    while i < len(text):
        m = ARG.match(text, i)
        if m and m.group(1) in names and (i == 0 or text[i - 1] != "$"):
            if lit:
                out.append((False, "".join(lit)))
                lit = []
            out.append((True, m.group(0)))
            i = m.end()
            continue
        lit.append(text[i])
        i += 1
    if lit:
        out.append((False, "".join(lit)))
    return out


def escape_text(text: str, after: str) -> str:
    """escape_text quotes the characters ICU MessageFormat reads: an
    apostrophe before a brace, and braces themselves."""
    out: list[str] = []
    for i, c in enumerate(text):
        if c == "'":
            nxt = text[i + 1] if i + 1 < len(text) else after
            out.append("''" if nxt in "{}" else "'")
        else:
            out.append(c)
    quoted = "".join(out)
    out, i = [], 0
    while i < len(quoted):
        if quoted[i] in "{}":
            j = i
            while j < len(quoted) and quoted[j] in "{}":
                j += 1
            out.append("'" + quoted[i:j] + "'")
            i = j
            continue
        out.append(quoted[i])
        i += 1
    return "".join(out)


def icu(text: str, names: set[str]) -> str:
    """icu writes text as an ICU message whose arguments are names."""
    parts = segments(text, names)
    out: list[str] = []
    for i, (is_arg, run) in enumerate(parts):
        if is_arg:
            out.append(run)
            continue
        after = ""
        if i + 1 < len(parts):
            after = "{" if parts[i + 1][0] else parts[i + 1][1][:1]
        out.append(escape_text(run, after))
    return "".join(out)


def arg_names(text: str) -> list[str]:
    """arg_names are the arguments text uses, in order, without repeats."""
    seen: dict[str, None] = {}
    for m in ARG.finditer(text):
        if m.start() > 0 and text[m.start() - 1] == "$":
            continue
        seen.setdefault(m.group(1))
    return list(seen)


def yaml_string(s: str) -> str:
    """yaml_string quotes s for a YAML double-quoted scalar."""
    out = ['"']
    for c in s:
        if c == "\\":
            out.append("\\\\")
        elif c == '"':
            out.append('\\"')
        elif c == "\n":
            out.append("\\n")
        elif c == "\t":
            out.append("\\t")
        elif c == "\r":
            out.append("\\r")
        elif ord(c) < 0x20 or ord(c) == 0x7F:
            out.append("\\u%04x" % ord(c))
        else:
            out.append(c)
    out.append('"')
    return "".join(out)


def arg_type(name: str) -> str:
    if name in INT_ARGS:
        return "int"
    if name in DOUBLE_ARGS:
        return "double"
    return "String"


def build(lang: str, lang_file: str, keys: list[str], names: dict[str, str],
          args: dict[str, list[str]], msgs: dict[str, str]) -> str:
    """build writes one language's YAML, every key in the source's order."""
    lines = [
        f"# {lang}: written by scripts/gen_locales.py from lib/i18n/messages/{lang_file}.",
        "# Do not edit it by hand: change the TypeScript messages, then run",
        "#   python3 scripts/gen_locales.py && go tool nsgo i18n",
        "",
    ]
    for key in keys:
        text = icu(msgs[key], set(args[key]))
        if args[key]:
            lines.append(f"{names[key]}:")
            lines.append(f"  message: {yaml_string(text)}")
            lines.append("  placeholders:")
            for arg in args[key]:
                lines.append(f"    {arg}: {{type: {arg_type(arg)}}}")
        else:
            lines.append(f"{names[key]}: {yaml_string(text)}")
    return "\n".join(lines) + "\n"


def main() -> int:
    msgs = {lang_file: parse_ts(SRC_DIR / lang_file) for lang_file, _ in LANGS}
    en = msgs[LANGS[0][0]]
    for lang_file, _ in LANGS[1:]:
        other = msgs[lang_file]
        if set(other) != set(en):
            missing = sorted(set(en) - set(other))
            extra = sorted(set(other) - set(en))
            raise SystemExit(f"{lang_file}: keys differ: missing {missing}, extra {extra}")

    # The identifiers must be unique case-insensitively: NSGo names a
    # function per message by capitalizing the key.
    seen: dict[str, str] = {}
    names: dict[str, str] = {}
    for key in en:
        name = ident_key(key)
        if other := seen.get(name.lower()):
            raise SystemExit(f"{key} and {other} make functions of the same name {name}")
        seen[name.lower()] = key
        names[key] = name

    reserved = {"source", "catalog", "messages", "in", "for", "translation", "supply", "parameters"}
    for key, name in names.items():
        if name.lower() in reserved:
            raise SystemExit(f"{key} makes {name}, which the package declares")

    # The source's arguments, which every language must stay within.
    args = {key: arg_names(en[key]) for key in en}
    for lang_file, _ in LANGS[1:]:
        for key in en:
            extra = [a for a in arg_names(msgs[lang_file][key]) if a not in args[key]]
            if extra:
                raise SystemExit(f"{lang_file}: {key} uses {extra}, which the source's message does not")

    OUT_DIR.mkdir(parents=True, exist_ok=True)
    for lang_file, lang in LANGS:
        text = build(lang, lang_file, list(en), names, args, msgs[lang_file])
        (OUT_DIR / f"{lang}.yaml").write_text(text, encoding="utf-8")
        print(f"wrote locales/{lang}.yaml: {len(en)} keys")

    KEY_MAP.write_text(
        json.dumps(names, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    print(f"wrote {KEY_MAP.relative_to(ROOT)}: {len(en)} keys")
    with_args = sum(1 for key in en if args[key])
    print(f"{with_args} messages take arguments, {len(en) - with_args} have none")
    return 0


if __name__ == "__main__":
    sys.exit(main())
