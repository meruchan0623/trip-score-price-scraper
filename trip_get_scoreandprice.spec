# -*- mode: python ; coding: utf-8 -*-

import sys
from pathlib import Path

project_dir = Path(SPECPATH)
root_dir = project_dir.parent
script_path = str(project_dir / 'main.py')
is_windows = sys.platform.startswith('win')
is_macos = sys.platform == 'darwin'
ico_icon = root_dir / 'tsimicon.ico'
icns_icon = root_dir / 'tsimicon.icns'
exe_icon = str(ico_icon) if is_windows and ico_icon.exists() else None
bundle_icon = str(icns_icon) if is_macos and icns_icon.exists() else None
use_upx = is_windows

a = Analysis(
    [script_path],
    pathex=[str(project_dir)],
    binaries=[],
    datas=[],
    hiddenimports=[],
    hookspath=[],
    hooksconfig={},
    runtime_hooks=[],
    excludes=[],
    noarchive=False,
    optimize=1,
)
pyz = PYZ(a.pure)

exe = EXE(
    pyz,
    a.scripts,
    [],
    exclude_binaries=True,
    name='trip_get_scoreandprice',
    debug=False,
    bootloader_ignore_signals=False,
    strip=is_macos,
    upx=use_upx,
    console=False,
    disable_windowed_traceback=False,
    argv_emulation=is_macos,
    target_arch=None,
    codesign_identity=None,
    entitlements_file=None,
    icon=exe_icon,
)
coll = COLLECT(
    exe,
    a.binaries,
    a.datas,
    strip=is_macos,
    upx=use_upx,
    upx_exclude=[],
    name='trip_get_scoreandprice',
)
app = BUNDLE(
    coll,
    name='trip_get_scoreandprice.app',
    icon=bundle_icon,
    bundle_identifier=None,
)
