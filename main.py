from __future__ import annotations

import multiprocessing
import sys

from cli import run_cli
from gui import run_gui


def main(argv: list[str] | None = None) -> int:
    multiprocessing.freeze_support()
    args = list(sys.argv[1:] if argv is None else argv)
    if not args or "--gui" in args:
        run_gui()
        return 0
    return run_cli(args)


if __name__ == "__main__":
    raise SystemExit(main())
