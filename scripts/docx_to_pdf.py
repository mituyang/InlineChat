#!/usr/bin/env python3
from __future__ import annotations

import argparse
import os
import platform
import shlex
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="本地将 docx 转为 pdf。")
    parser.add_argument("input", help="输入的 .docx 文件路径")
    parser.add_argument(
        "-o",
        "--output",
        help="输出的 .pdf 文件路径，默认与输入文件同目录同名",
    )
    parser.add_argument(
        "--engine",
        choices=("auto", "word", "libreoffice"),
        default="auto",
        help="转换引擎，默认 auto",
    )
    return parser.parse_args()


def ensure_docx_file(path: Path) -> Path:
    if not path.exists():
        raise FileNotFoundError(f"文件不存在: {path}")
    if not path.is_file():
        raise ValueError(f"不是文件: {path}")
    if path.suffix.lower() != ".docx":
        raise ValueError(f"只支持 .docx 文件: {path}")
    return path.resolve()


def resolve_output_path(input_path: Path, output_arg: str | None) -> Path:
    if output_arg:
        output_path = Path(output_arg).expanduser()
    else:
        output_path = input_path.with_suffix(".pdf")

    if output_path.suffix.lower() != ".pdf":
        output_path = output_path.with_suffix(".pdf")

    return output_path.resolve()


def shell_quote(value: str) -> str:
    return shlex.quote(value)


def word_available() -> bool:
    if platform.system() != "Darwin":
        return False
    return Path("/Applications/Microsoft Word.app").exists()


def soffice_available() -> bool:
    return shutil.which("soffice") is not None


def run_command(cmd: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(cmd, check=False, capture_output=True, text=True)


def convert_with_word(input_path: Path, output_path: Path) -> None:
    script = [
        'tell application "Microsoft Word"',
        "set display alerts to none",
        f'set inputFile to POSIX file "{input_path.as_posix()}"',
        f'set outputFile to "{output_path.as_posix()}"',
        "open inputFile",
        "set docRef to active document",
        "save as docRef file name outputFile file format format PDF",
        "close docRef saving no",
        "end tell",
    ]
    cmd = ["osascript"]
    for line in script:
        cmd.extend(["-e", line])

    result = run_command(cmd)
    if result.returncode != 0:
        stderr = result.stderr.strip() or result.stdout.strip() or "未知错误"
        raise RuntimeError(f"Word 转换失败: {stderr}")

    if not output_path.exists():
        raise RuntimeError(f"Word 未生成输出文件: {output_path}")


def convert_with_libreoffice(input_path: Path, output_path: Path) -> None:
    soffice = shutil.which("soffice")
    if not soffice:
        raise RuntimeError("未找到 soffice，可安装 LibreOffice 后重试")

    with tempfile.TemporaryDirectory(prefix="docx-to-pdf-") as tmpdir:
        tmp_output_dir = Path(tmpdir)
        cmd = [
            soffice,
            "--headless",
            "--convert-to",
            "pdf",
            str(input_path),
            "--outdir",
            str(tmp_output_dir),
        ]
        result = run_command(cmd)
        if result.returncode != 0:
            stderr = result.stderr.strip() or result.stdout.strip() or "未知错误"
            raise RuntimeError(f"LibreOffice 转换失败: {stderr}")

        generated = tmp_output_dir / f"{input_path.stem}.pdf"
        if not generated.exists():
            details = result.stdout.strip() or "未找到输出文件"
            raise RuntimeError(f"LibreOffice 未生成输出文件: {details}")

        output_path.parent.mkdir(parents=True, exist_ok=True)
        shutil.move(str(generated), str(output_path))


def convert(input_path: Path, output_path: Path, engine: str) -> str:
    if engine == "word":
        if not word_available():
            raise RuntimeError("当前环境不可用 Microsoft Word")
        convert_with_word(input_path, output_path)
        return "word"

    if engine == "libreoffice":
        if not soffice_available():
            raise RuntimeError("当前环境不可用 LibreOffice")
        convert_with_libreoffice(input_path, output_path)
        return "libreoffice"

    if word_available():
        try:
            convert_with_word(input_path, output_path)
            return "word"
        except Exception:
            if output_path.exists():
                output_path.unlink()

    if soffice_available():
        convert_with_libreoffice(input_path, output_path)
        return "libreoffice"

    raise RuntimeError("未找到可用转换器：Microsoft Word 或 LibreOffice")


def main() -> int:
    args = parse_args()

    try:
        input_path = ensure_docx_file(Path(args.input).expanduser())
        output_path = resolve_output_path(input_path, args.output)
        output_path.parent.mkdir(parents=True, exist_ok=True)

        engine = convert(input_path, output_path, args.engine)
        print(f"已生成: {output_path}")
        print(f"引擎: {engine}")
        return 0
    except Exception as exc:
        print(str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
