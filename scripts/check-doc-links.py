#!/usr/bin/env python3
"""저장소 안 마크다운 문서의 내부 링크·이미지 참조 무결성을 검사한다.

추적되는 모든 `.md` 문서를 훑어, 저장소 안 다른 파일을 가리키는 상대 경로
링크(`[text](path)`)와 이미지 참조(`![alt](path)`·`<img src="path">`)가
실제로 존재하는 파일을 가리키는지 확인한다. 깨진 참조가 하나라도 있으면
종료 코드 1 로 끝나므로, CI 머지 게이트(`.github/workflows/docs.yml`)와
기여자의 로컬 검증에 그대로 쓸 수 있다.

검사 대상이 아닌 것:
- 외부 URL(`http://`·`https://`·`mailto:`·`tel:`·`data:`): 네트워크에 의존해
  flaky 하므로 이 결정론적 게이트에서는 다루지 않는다. 외부 링크 상태는 별도로
  점검한다.
- 같은 문서 안 앵커(`#heading`)만 가리키는 링크: 파일 참조가 아니다.
- fenced code block(``` 또는 ~~~ 로 감싼 블록) 안의 예시 링크: 실제 참조가
  아니라 코드 예시이므로 건너뛴다.

표준 라이브러리만 쓰고 네트워크에 접속하지 않는다. 실행: `python3 -I scripts/check-doc-links.py`
"""
import os
import re
import subprocess
import sys

# [text](path) 중 이미지(`![...]`)가 아닌 링크. 경로는 공백 전까지(제목 "..." 제외).
MD_LINK = re.compile(r"(?<!\!)\[[^\]]*\]\(([^)\s]+)")
# ![alt](path) 이미지 링크.
MD_IMAGE = re.compile(r"!\[[^\]]*\]\(([^)\s]+)")
# <img ... src="path" ...> HTML 이미지.
HTML_IMAGE = re.compile(r"<img[^>]*\bsrc=[\"']([^\"']+)[\"']", re.IGNORECASE)

EXTERNAL_PREFIXES = ("http://", "https://", "mailto:", "tel:", "data:", "#")


def is_external(target: str) -> bool:
    return target.startswith(EXTERNAL_PREFIXES)


def main() -> int:
    root = subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True
    ).strip()
    tracked = subprocess.check_output(
        ["git", "ls-files"], cwd=root, text=True
    ).splitlines()
    md_files = [f for f in tracked if f.endswith(".md")]

    checked = 0
    broken = []
    for md in md_files:
        with open(os.path.join(root, md), encoding="utf-8", errors="ignore") as fh:
            lines = fh.readlines()
        base_dir = os.path.dirname(md)
        in_fence = False
        for lineno, line in enumerate(lines, 1):
            stripped = line.lstrip()
            if stripped.startswith("```") or stripped.startswith("~~~"):
                in_fence = not in_fence
                continue
            if in_fence:
                continue
            for pattern in (MD_LINK, MD_IMAGE, HTML_IMAGE):
                for match in pattern.finditer(line):
                    target = match.group(1).strip()
                    if is_external(target):
                        continue
                    path = target.split("#", 1)[0].split("?", 1)[0]
                    if not path:
                        continue
                    checked += 1
                    resolved = os.path.normpath(os.path.join(root, base_dir, path))
                    if not os.path.exists(resolved):
                        broken.append((md, lineno, target))

    print(f"추적 마크다운 {len(md_files)}개 · 내부 링크·이미지 참조 {checked}개 검사")
    if broken:
        print(f"깨진 참조 {len(broken)}개 — 가리키는 파일이 저장소에 없습니다:")
        for md, lineno, target in broken:
            print(f"  {md}:{lineno} -> {target}")
        return 1
    print("깨진 참조 0 — 모든 내부 링크·이미지가 실존 파일을 가리킵니다.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
