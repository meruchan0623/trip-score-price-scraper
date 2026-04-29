from __future__ import annotations

import importlib.machinery
import importlib.util
from pathlib import Path

LEGACY_FILE = Path(__file__).with_name("trip_get_scoreandprice.pyw")


def _load_legacy_module():
    loader = importlib.machinery.SourceFileLoader("trip_get_scoreandprice_legacy_gui", str(LEGACY_FILE))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


def run_gui() -> None:
    legacy = _load_legacy_module()
    root = legacy.tk.Tk()
    app = legacy.WebScraperApp(root)
    root.protocol("WM_DELETE_WINDOW", app.on_closing)
    root.mainloop()
