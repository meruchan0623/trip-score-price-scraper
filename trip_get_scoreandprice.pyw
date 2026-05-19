import tkinter as tk
from tkinter import ttk, scrolledtext, messagebox, filedialog
import threading
import time
import re
import csv
import sys
import platform
from pathlib import Path
from datetime import datetime
import os
import queue
from itertools import product
import subprocess
import zipfile
import io
import shutil
import requests
import urllib3
import ctypes
import json
import gc
import uuid
import tempfile

try:
    import psutil
except ImportError:
    psutil = None

# 禁用SSL警告
urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)

# ========== 屏保/锁屏禁用功能 ==========
class ScreenSaverPreventer:
    """防止屏保和锁屏的跨平台类"""
    
    def __init__(self):
        self.current_os = platform.system()
        self.caffeinate_process = None
        self._is_preventing = False
    
    def start_prevention(self):
        """开始禁用屏保和锁屏"""
        if self._is_preventing:
            return True
        
        try:
            if self.current_os == "Windows":
                return self._prevent_windows()
            elif self.current_os == "Darwin":
                return self._prevent_mac()
            else:
                return False
        except Exception as e:
            print(f"启用屏保禁用失败: {e}")
            return False
    
    def stop_prevention(self):
        """停止禁用屏保和锁屏"""
        if not self._is_preventing:
            return
        
        try:
            if self.current_os == "Windows":
                self._restore_windows()
            elif self.current_os == "Darwin":
                self._restore_mac()
        except Exception as e:
            print(f"恢复屏保设置失败: {e}")
        finally:
            self._is_preventing = False
    
    def _prevent_windows(self):
        """Windows: 使用SetThreadExecutionState禁用屏保和锁屏"""
        try:
            # ES_CONTINUOUS | ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED
            # 0x80000000 | 0x00000001 | 0x00000002 = 0x80000003
            ES_CONTINUOUS = 0x80000000
            ES_SYSTEM_REQUIRED = 0x00000001
            ES_DISPLAY_REQUIRED = 0x00000002
            
            ctypes.windll.kernel32.SetThreadExecutionState(
                ES_CONTINUOUS | ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED
            )
            self._is_preventing = True
            return True
        except Exception as e:
            print(f"Windows屏保禁用失败: {e}")
            return False
    
    def _restore_windows(self):
        """Windows: 恢复正常电源管理"""
        try:
            ES_CONTINUOUS = 0x80000000
            ctypes.windll.kernel32.SetThreadExecutionState(ES_CONTINUOUS)
        except:
            pass
    
    def _prevent_mac(self):
        """macOS: 使用caffeinate命令禁用屏保"""
        try:
            # caffeinate -dims: 禁用空闲睡眠、磁盘睡眠、显示器睡眠、系统睡眠
            self.caffeinate_process = subprocess.Popen(
                ['caffeinate', '-dims'],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL
            )
            self._is_preventing = True
            return True
        except Exception as e:
            print(f"macOS屏保禁用失败: {e}")
            return False
    
    def _restore_mac(self):
        """macOS: 终止caffeinate进程"""
        try:
            if self.caffeinate_process:
                self.caffeinate_process.terminate()
                self.caffeinate_process.wait(timeout=2)
                self.caffeinate_process = None
        except:
            if self.caffeinate_process:
                try:
                    self.caffeinate_process.kill()
                except:
                    pass
                self.caffeinate_process = None
    
    def __del__(self):
        """析构时确保恢复设置"""
        self.stop_prevention()

# 第三方库依赖
# 请通过以下命令安装：
# pip install undetected-chromedriver
# pip install openpyxl
import undetected_chromedriver as uc
import openpyxl
from openpyxl.utils import get_column_letter
from openpyxl.styles import Font, Alignment

# Selenium imports
from selenium import webdriver
from selenium.webdriver.chrome.service import Service as ChromeService
from selenium.webdriver.chrome.options import Options as ChromeOptions
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from selenium.common.exceptions import NoSuchElementException, TimeoutException, WebDriverException
from selenium.webdriver.common.action_chains import ActionChains

# ========== 配置管理器 ==========
class SessionManager:
    """管理浏览器会话数据的保存和加载（Cookie、本地存储等）"""
    
    def __init__(self):
        self.session_file = self._get_session_path()
    
    def _get_session_path(self):
        """获取会话文件路径，与trip_detail_filling.pyw共享"""
        if platform.system() == "Windows":
            config_dir = os.path.join(os.environ.get('PROGRAMDATA', 'C:\\ProgramData'), 'TripDetailFilling')
        elif platform.system() == "Darwin":
            config_dir = os.path.join(os.path.expanduser('~'), 'Library', 'Preferences', 'TripDetailFilling')
        else:
            config_dir = os.path.join(os.path.expanduser('~'), '.config', 'TripDetailFilling')
        
        try:
            os.makedirs(config_dir, exist_ok=True)
        except:
            config_dir = os.path.expanduser('~')
        
        return os.path.join(config_dir, 'session_data.json')
    
    def save_session(self, driver):
        """保存浏览器会话数据（Cookies 和本地存储）"""
        try:
            session_data = {
                'cookies': driver.get_cookies(),
                'local_storage': driver.execute_script('return window.localStorage;') or {},
                'session_storage': driver.execute_script('return window.sessionStorage;') or {},
                'timestamp': datetime.now().isoformat()
            }
            
            with open(self.session_file, 'w', encoding='utf-8') as f:
                json.dump(session_data, f, indent=2, ensure_ascii=False)
            
            print(f"✓ 会话数据已保存: {self.session_file}")
            return True
        except Exception as e:
            print(f"✗ 保存会话数据失败: {e}")
            return False
    
    def load_session(self, driver):
        """加载保存的会话数据到浏览器"""
        try:
            if not os.path.exists(self.session_file):
                print(f"会话文件不存在: {self.session_file}")
                return False
            
            with open(self.session_file, 'r', encoding='utf-8') as f:
                session_data = json.load(f)
            
            # 加载 Cookies
            if 'cookies' in session_data:
                for cookie in session_data['cookies']:
                    try:
                        # 移除某些不能设置的属性
                        cookie.pop('expiry', None)
                        driver.add_cookie(cookie)
                    except Exception as e:
                        print(f"加载Cookie失败 {cookie.get('name', '未知')}: {e}")
            
            print(f"✓ 会话数据已加载: {self.session_file}")
            return True
        except Exception as e:
            print(f"✗ 加载会话数据失败: {e}")
            return False

class WebScraperApp:
    def __init__(self, root):
        self.root = root
        self.root.title("Trip商品信息价格提取工具v1.0 Build 260317")
        self.root.geometry("1300x850")

        # --- Instance Variables ---
        self.current_os = platform.system()
        self.driver = None
        self.scraping_thread = None
        self.is_paused = threading.Event()
        self.is_stopped = threading.Event()
        self.urls_to_process = []
        self.is_paused.set()
        
        # 浏览器状态管理
        self.browser_state = "CLOSED"  # CLOSED, OPENING, OPEN, CLOSING
        self.browser_lock = threading.Lock()
        
        # SKU层级跟踪
        self.max_sku_layers = 0
        
        # 实时结果数据
        self.results_data = []
        
        # 屏保禁用器
        self.screen_saver_preventer = ScreenSaverPreventer()
        
        # 会话管理器
        self.session_manager = SessionManager()

        # 临时用户数据目录（支持多开）
        self._temp_user_data_dir = None

        # 浏览器内存保护配置
        self.browser_memory_guard_enabled = True
        self.browser_memory_limit_mb = 8192
        self.browser_restart_every = 20
        self._processed_since_browser_restart = 0
        self._browser_restart_count = 0
        self.cli_headless = False
        self.cli_output_path = None
        self.cli_output_dir = None
        self.last_saved_filepath = None
        self.last_save_error = None
        self.include_vendor_column = False
        self.excel_input_path = None
        self.vendor_workers_var = tk.IntVar(value=5)

        # --- GUI Setup ---
        self._create_widgets()
        self.log("程序已就绪。")
        self.log("程序将自动尝试下载和管理浏览器驱动程序。")
        self.log("功能说明：")
        self.log("  - 默认模式：抓取评分+SKU价格组合数据")
        self.log("  - 仅抓取评分：勾选复选框后仅获取评分数据，跳过SKU价格")
        self.log("  - 实价抓取：勾选后抓取SKU选择后的动态加载实价")
        self.log("  - 商品资讯：勾选后抓取产品详情页的设备信息")
        self.log("  - 自动禁用屏保：打开浏览器后自动禁用Windows锁屏/macOS屏保")
        self.log("  - 浏览器内存保护：默认超过8GB或每20条自动重启Chrome释放内存")
        self.log("请点击 [打开浏览器] 开始任务。")

    def _create_widgets(self):
        # --- Control Frame ---
        control_frame = ttk.Frame(self.root, padding="10")
        control_frame.pack(fill=tk.X, side=tk.TOP)
        
        # Buttons
        self.btn_open_browser = ttk.Button(control_frame, text="打开浏览器", command=self.open_browser)
        self.btn_open_browser.grid(row=0, column=0, padx=5, pady=5)
        
        self.btn_start = ttk.Button(control_frame, text="开始执行", command=self.start_scraping)
        self.btn_start.grid(row=0, column=1, padx=5, pady=5)
        
        self.btn_pause_resume = ttk.Button(control_frame, text="暂停执行", command=self.pause_resume_scraping, state=tk.DISABLED)
        self.btn_pause_resume.grid(row=0, column=2, padx=5, pady=5)
        
        self.btn_stop = ttk.Button(control_frame, text="停止执行", command=self.stop_scraping, state=tk.DISABLED)
        self.btn_stop.grid(row=0, column=3, padx=5, pady=5)
        
        self.btn_close_browser = ttk.Button(control_frame, text="关闭浏览器", command=self.close_browser)
        self.btn_close_browser.grid(row=0, column=4, padx=5, pady=5)
        
        self.btn_export = ttk.Button(control_frame, text="导出到Excel", command=self.manual_export)
        self.btn_export.grid(row=0, column=5, padx=5, pady=5)
        
        self.btn_clear = ttk.Button(control_frame, text="清空数据", command=self.clear_data, state=tk.DISABLED)
        self.btn_clear.grid(row=0, column=6, padx=5, pady=5)
        
        # Checkboxes for options
        options_frame = ttk.LabelFrame(control_frame, text="抓取选项", padding="5")
        options_frame.grid(row=1, column=0, columnspan=6, padx=5, pady=5, sticky=tk.W+tk.E)
        
        self.rating_only_var = tk.BooleanVar(value=False)
        self.chk_rating_only = ttk.Checkbutton(options_frame, text="仅抓取评分", variable=self.rating_only_var)
        self.chk_rating_only.grid(row=0, column=0, padx=10, pady=2, sticky=tk.W)
        
        self.extract_real_price_var = tk.BooleanVar(value=True)
        self.chk_extract_real_price = ttk.Checkbutton(options_frame, text="抓取实价（SKU动态价格）", variable=self.extract_real_price_var)
        self.chk_extract_real_price.grid(row=0, column=1, padx=10, pady=2, sticky=tk.W)
        
        self.extract_product_info_var = tk.BooleanVar(value=True)
        self.chk_extract_product_info = ttk.Checkbutton(options_frame, text="抓取商品资讯（设备信息）", variable=self.extract_product_info_var)
        self.chk_extract_product_info.grid(row=0, column=2, padx=10, pady=2, sticky=tk.W)
        
        # Progress Label
        self.progress_label = ttk.Label(control_frame, text="进度: 0/0", font=("Arial", 10, "bold"))
        self.progress_label.grid(row=0, column=7, padx=15, pady=5)
        
        # URL Frame
        url_frame = ttk.Frame(self.root, padding="10")
        url_frame.pack(fill=tk.BOTH, expand=False, side=tk.TOP)
        
        ttk.Label(url_frame, text="在此处输入批量网址 (每行一个):").pack(fill=tk.X, anchor=tk.W)
        self.url_text = scrolledtext.ScrolledText(url_frame, height=6, width=80)
        self.url_text.pack(fill=tk.BOTH, expand=True)

        excel_frame = ttk.Frame(url_frame)
        excel_frame.pack(fill=tk.X, pady=(6, 0))
        self.btn_choose_excel = ttk.Button(excel_frame, text="选择Excel", command=self.choose_excel_input)
        self.btn_choose_excel.pack(side=tk.LEFT, padx=(0, 5))
        self.btn_clear_excel = ttk.Button(excel_frame, text="清除Excel", command=self.clear_excel_input)
        self.btn_clear_excel.pack(side=tk.LEFT, padx=(0, 10))
        ttk.Label(excel_frame, text="并发浏览器数:").pack(side=tk.LEFT)
        self.spin_workers = ttk.Spinbox(excel_frame, from_=1, to=5, width=4, textvariable=self.vendor_workers_var)
        self.spin_workers.pack(side=tk.LEFT, padx=(5, 10))
        self.excel_file_label = ttk.Label(excel_frame, text="未选择Excel（选择后优先使用Excel输入）")
        self.excel_file_label.pack(side=tk.LEFT, fill=tk.X, expand=True)
        
        # Results TreeView Frame
        results_frame = ttk.LabelFrame(self.root, text="抓取结果实时预览", padding="10")
        results_frame.pack(fill=tk.BOTH, expand=True, side=tk.TOP)
        
        # Create TreeView with scrollbars
        tree_scroll_y = ttk.Scrollbar(results_frame, orient=tk.VERTICAL)
        tree_scroll_x = ttk.Scrollbar(results_frame, orient=tk.HORIZONTAL)
        
        self.results_tree = ttk.Treeview(results_frame, 
                                         yscrollcommand=tree_scroll_y.set,
                                         xscrollcommand=tree_scroll_x.set,
                                         show='tree headings',
                                         height=10)
        
        tree_scroll_y.config(command=self.results_tree.yview)
        tree_scroll_x.config(command=self.results_tree.xview)
        
        tree_scroll_y.pack(side=tk.RIGHT, fill=tk.Y)
        tree_scroll_x.pack(side=tk.BOTTOM, fill=tk.X)
        self.results_tree.pack(fill=tk.BOTH, expand=True)
        
        # Initialize TreeView columns (will be updated dynamically)
        self._setup_treeview_columns()
        
        # Log Frame
        log_frame = ttk.Frame(self.root, padding="10")
        log_frame.pack(fill=tk.BOTH, expand=True, side=tk.BOTTOM)
        
        ttk.Label(log_frame, text="调试与日志信息:").pack(fill=tk.X, anchor=tk.W)
        self.log_text = scrolledtext.ScrolledText(log_frame, height=12, width=80, state=tk.DISABLED)
        self.log_text.pack(fill=tk.BOTH, expand=True)

    def log(self, message):
        def _append_log():
            self.log_text.config(state=tk.NORMAL)
            self.log_text.insert(tk.END, f"[{datetime.now().strftime('%H:%M:%S')}] {message}\n")
            self.log_text.see(tk.END)
            self.log_text.config(state=tk.DISABLED)
        self.root.after(0, _append_log)
    
    def update_progress(self, current, total):
        """更新任务进度显示"""
        def _update():
            self.progress_label.config(text=f"进度: {current}/{total}")
        self.root.after(0, _update)

    def choose_excel_input(self):
        path = filedialog.askopenfilename(
            title="选择厂商链接Excel",
            filetypes=[("Excel files", "*.xlsx *.xlsm"), ("All files", "*.*")]
        )
        if path:
            self.excel_input_path = path
            self.excel_file_label.config(text=path)
            self.include_vendor_column = True
            self._setup_treeview_columns()
            self.log(f"已选择Excel输入: {path}")

    def clear_excel_input(self):
        self.excel_input_path = None
        self.excel_file_label.config(text="未选择Excel（选择后优先使用Excel输入）")
        self.include_vendor_column = False
        self._setup_treeview_columns()
        self.log("已清除Excel输入，将使用文本框网址。")
    
    def _setup_treeview_columns(self):
        """设置TreeView的列 - 真正动态显示有数据的列"""
        # 总是显示的基本列
        base_columns = ['ID', 'URL', '商品名称']
        if getattr(self, "include_vendor_column", False):
            base_columns.insert(0, '厂商')
        
        # 评分列（总是显示，调整顺序：预定数 -> 评价数 -> 评分 -> ...）
        rating_columns = ['预定数', '评价数', '评分', '客户服务', '方便程度', '讯号状态', '中差评价数']
        
        # 动态列
        dynamic_columns = []
        
        # SKU列（只在非仅评分模式下显示）
        if not self.rating_only_var.get() and self.max_sku_layers > 0:
            for i in range(self.max_sku_layers):
                dynamic_columns.append(f'SKU{i+1}')
        
        # 价格列（只在非仅评分模式下显示）
        if not self.rating_only_var.get():
            dynamic_columns.extend(['货币', '外显价格'])
            if self.extract_real_price_var.get():
                dynamic_columns.append('实价')
            dynamic_columns.append('促销标签')
        
        # 商品资讯列（只在启用时显示）
        if self.extract_product_info_var.get():
            dynamic_columns.append('商品资讯')
        
        # 组合所有列：基本 + 动态 + 评分
        all_columns = base_columns + dynamic_columns + rating_columns
        
        # 配置TreeView
        self.results_tree['columns'] = all_columns
        self.results_tree.column('#0', width=0, stretch=tk.NO)
        
        # 设置列宽度和标题
        column_widths = {
            'ID': 100,
            '厂商': 120,
            'URL': 100,
            '商品名称': 200,
            '货币': 50,
            '外显价格': 80,
            '实价': 80,
            '促销标签': 100,
            '评分': 50,
            '评价数': 70,
            '预定数': 70,
            '客户服务': 80,
            '方便程度': 80,
            '讯号状态': 80,
            '中差评价数': 90,
            '商品资讯': 150
        }
        
        for col in all_columns:
            width = column_widths.get(col, 100)
            if col.startswith('SKU'):
                width = 120
            self.results_tree.column(col, width=width, anchor=tk.W)
            self.results_tree.heading(col, text=col, anchor=tk.W)
    
    def _add_result_to_treeview(self, result_data):
        """将结果添加到TreeView"""
        def _insert():
            # 准备数据
            values = []
            for col in self.results_tree['columns']:
                value = result_data.get(col, '')
                # 截断过长的文本
                if isinstance(value, str) and len(value) > 100:
                    value = value[:97] + '...'
                values.append(value)
            
            # 插入数据
            self.results_tree.insert('', tk.END, values=values)
            
            # 滚动到最后
            children = self.results_tree.get_children()
            if children:
                self.results_tree.see(children[-1])
        
        self.root.after(0, _insert)

    def open_browser(self):
        """
        打开浏览器 - 使用多线程避免GUI卡死，带状态管理
        """
        with self.browser_lock:
            if self.browser_state == "OPEN":
                self.log("浏览器已经打开。")
                return
            elif self.browser_state == "OPENING":
                self.log("浏览器正在启动中，请稍候...")
                return
            elif self.browser_state == "CLOSING":
                self.log("浏览器正在关闭中，请稍候...")
                return
            
            # 设置状态为OPENING
            self.browser_state = "OPENING"
        
        # 禁用按钮防止重复点击
        self.btn_open_browser.config(state=tk.DISABLED, text="正在启动...")
        
        # 在后台线程中初始化浏览器
        browser_thread = threading.Thread(target=self._initialize_browser_thread, daemon=True)
        browser_thread.start()

    def _initialize_browser_thread(self):
        """在后台线程中初始化浏览器 - 参考trip_outerlist_scraping.pyw的策略"""
        try:
            self.log("正在初始化Chrome浏览器...")
            self.log(f"检测到操作系统: {self.current_os}")
            
            # 检测Chrome浏览器
            chrome_version = self._get_chrome_version()
            if chrome_version:
                self.log(f"检测到Chrome浏览器版本: {chrome_version}")
            else:
                self.log("⚠ 无法检测Chrome版本，请确保已安装Google Chrome浏览器")
            
            # 检测WebDriver
            self.log("正在检测WebDriver...")
            if self.current_os == "Darwin":
                chrome_major = chrome_version.split('.')[0] if chrome_version else None
                existing_driver = self._find_existing_chromedriver(chrome_major)
                if existing_driver:
                    self.log(f"✓ 找到版本匹配的ChromeDriver: {existing_driver}")
                else:
                    self.log(f"未找到匹配Chrome {chrome_major}的ChromeDriver，将自动下载")
            
            self._initialize_driver_for_current_os()
            
            if self.driver:
                # 设置页面加载超时
                self.driver.set_page_load_timeout(30)
                self.driver.implicitly_wait(10)
                self._log_memory_guard_config()
                
                # 启用屏保禁用
                if self.screen_saver_preventer.start_prevention():
                    self.log("✓ 已禁用屏保和锁屏")
                else:
                    self.log("⚠ 无法禁用屏保和锁屏")
                
                # 更新状态
                with self.browser_lock:
                    self.browser_state = "OPEN"
                
                # 在主线程中更新GUI
                self.root.after(0, self._browser_initialized_callback, True, "浏览器启动成功！可以开始执行任务。")
            else:
                raise Exception("浏览器初始化返回None")
            
        except Exception as e:
            self.log(f"浏览器初始化失败: {e}")
            # 重置状态
            with self.browser_lock:
                self.browser_state = "CLOSED"
            # 在主线程中显示错误
            self.root.after(0, self._browser_initialized_callback, False, str(e))

    def _initialize_driver_for_current_os(self):
        """根据当前系统初始化Chrome Driver。"""
        if self.current_os == "Darwin":  # macOS
            self._initialize_chrome_browser_mac()
        elif self.current_os == "Windows":  # Windows
            self._initialize_chrome_browser_windows()
        else:
            raise Exception(f"不支持的操作系统: {self.current_os}")

    def _configure_driver_after_restart(self):
        """内存保护重启后恢复Driver基础设置。"""
        if not self.driver:
            raise Exception("浏览器重启失败：Driver为空")
        self.driver.set_page_load_timeout(30)
        self.driver.implicitly_wait(10)

    def _log_memory_guard_config(self):
        if not self.browser_memory_guard_enabled:
            self.log("浏览器内存保护已关闭。")
            return
        psutil_state = "可用" if psutil else "不可用，仅按处理数量重启"
        self.log(
            f"浏览器内存保护已启用：超过 {self.browser_memory_limit_mb}MB "
            f"或每 {self.browser_restart_every} 条URL自动重启Chrome（psutil: {psutil_state}）。"
        )
    
    def _create_temp_user_data_dir(self):
        """创建临时用户数据目录，支持多实例同时运行"""
        unique_dir = os.path.join(tempfile.gettempdir(), f"chrome_trip_{uuid.uuid4().hex[:12]}")
        os.makedirs(unique_dir, exist_ok=True)
        self._temp_user_data_dir = unique_dir
        self.log(f"创建临时用户数据目录: {unique_dir}")
        return unique_dir

    def _add_memory_optimization_args(self, options):
        """为Chrome选项添加内存优化参数"""
        memory_args = [
            "--disable-background-networking",
            "--disable-default-apps",
            "--disable-sync",
            "--disable-translate",
            "--disable-background-timer-throttling",
            "--disable-backgrounding-occluded-windows",
            "--disable-renderer-backgrounding",
            "--disable-ipc-flooding-protection",
            "--disable-client-side-phishing-detection",
            "--disable-component-update",
            "--disable-domain-reliability",
            "--disable-hang-monitor",
            "--disable-prompt-on-repost",
            "--disable-application-cache",
            "--disable-extensions",
            "--disable-features=TranslateUI,BackForwardCache",
            "--no-first-run",
            "--safebrowsing-disable-auto-update",
            "--aggressive-cache-discard",
            "--disk-cache-size=104857600",
            "--media-cache-size=52428800",
            "--js-flags=--max-old-space-size=512",
        ]
        for arg in memory_args:
            options.add_argument(arg)

    def _apply_cli_browser_options(self, options):
        """Apply optional CLI-only browser settings without changing GUI defaults."""
        if not getattr(self, "cli_headless", False):
            return
        options.add_argument("--headless=new")
        options.add_argument("--window-size=1920,1080")
        options.add_argument("--disable-gpu")
        self.log("CLI headless模式已启用。")

    def _clear_browser_memory(self):
        """清理浏览器内存 - 在每个URL处理完毕后调用"""
        if not self.driver:
            gc.collect()
            return
        try:
            self.driver.execute_script("window.stop();")
        except:
            pass
        try:
            self.driver.execute_cdp_cmd('Memory.forciblyPurgeJavaScriptMemory', {})
        except:
            pass
        try:
            self.driver.execute_cdp_cmd('Network.clearBrowserCache', {})
        except:
            pass
        try:
            self.driver.execute_cdp_cmd('HeapProfiler.collectGarbage', {})
        except:
            pass
        gc.collect()

    def _get_driver_service_pid(self, driver=None):
        """获取当前ChromeDriver服务进程PID。"""
        driver = driver or self.driver
        if not driver:
            return None
        try:
            service = getattr(driver, "service", None)
            process = getattr(service, "process", None)
            return getattr(process, "pid", None)
        except:
            return None

    def _collect_process_tree(self, pid):
        if not psutil or not pid:
            return []
        try:
            parent = psutil.Process(pid)
            return [parent] + parent.children(recursive=True)
        except:
            return []

    def _iter_managed_browser_processes(self, driver=None):
        """列出本程序创建的Chrome/ChromeDriver进程，避免影响用户自己的Chrome。"""
        if not psutil:
            return []

        processes = {}
        for proc in self._collect_process_tree(self._get_driver_service_pid(driver)):
            processes[proc.pid] = proc

        temp_dir = self._temp_user_data_dir
        if temp_dir:
            for proc in psutil.process_iter(["pid", "name", "cmdline"]):
                try:
                    cmdline = " ".join(proc.info.get("cmdline") or [])
                    if temp_dir in cmdline:
                        processes[proc.pid] = proc
                        for child in proc.children(recursive=True):
                            processes[child.pid] = child
                except:
                    continue

        return list(processes.values())

    def _managed_browser_rss_mb(self):
        """统计本程序管理的Chrome相关进程RSS。"""
        if not psutil:
            return None
        total = 0
        for proc in self._iter_managed_browser_processes():
            try:
                total += proc.memory_info().rss
            except:
                pass
        return total / (1024 * 1024)

    def _terminate_managed_browser_processes(self, driver=None, timeout=5):
        """强制清理本程序创建的残留Chrome进程。"""
        if not psutil:
            return 0

        current_pid = os.getpid()
        processes = []
        for proc in self._iter_managed_browser_processes(driver):
            try:
                if proc.pid != current_pid and proc.is_running():
                    processes.append(proc)
            except:
                pass

        if not processes:
            return 0

        for proc in processes:
            try:
                proc.terminate()
            except:
                pass

        try:
            _, alive = psutil.wait_procs(processes, timeout=timeout)
        except:
            alive = processes

        killed = 0
        for proc in alive:
            try:
                proc.kill()
                killed += 1
            except:
                pass

        if killed:
            try:
                psutil.wait_procs(alive, timeout=2)
            except:
                pass
        return len(processes)

    def _cleanup_temp_user_data_dir(self):
        if self._temp_user_data_dir and os.path.exists(self._temp_user_data_dir):
            try:
                shutil.rmtree(self._temp_user_data_dir, ignore_errors=True)
                self.log(f"已清理临时用户数据目录: {self._temp_user_data_dir}")
            except Exception as e:
                self.log(f"清理临时目录失败（可忽略）: {e}")
        self._temp_user_data_dir = None

    def _close_extra_tabs(self, keep_handle=None):
        """关闭多余标签页，确保每条URL结束后只保留主窗口。"""
        if not self.driver:
            return None
        try:
            handles = list(self.driver.window_handles)
        except:
            return None
        if not handles:
            return None

        keep_handle = keep_handle if keep_handle in handles else handles[0]
        for handle in handles:
            if handle == keep_handle:
                continue
            try:
                self.driver.switch_to.window(handle)
                self.driver.close()
            except Exception as e:
                self.log(f"关闭多余选项卡时出错（可忽略）: {e}")

        try:
            self.driver.switch_to.window(keep_handle)
            return keep_handle
        except:
            return None

    def _shutdown_driver(self, stop_screen_prevention=True, force_process_cleanup=True):
        """关闭当前Driver并清理临时Profile。"""
        driver_to_close = self.driver
        self.driver = None

        if stop_screen_prevention:
            try:
                self.screen_saver_preventer.stop_prevention()
                self.log("✓ 已恢复屏保和锁屏设置")
            except:
                pass

        if driver_to_close:
            quit_done = threading.Event()
            quit_error = []

            def quit_driver():
                try:
                    driver_to_close.quit()
                except Exception as exc:
                    quit_error.append(exc)
                finally:
                    quit_done.set()

            threading.Thread(target=quit_driver, daemon=True).start()
            if quit_done.wait(timeout=15):
                if quit_error:
                    self.log(f"正常关闭失败，将强制清理浏览器进程: {quit_error[0]}")
                else:
                    self.log("浏览器已成功关闭。")
            else:
                self.log("浏览器关闭超时，将强制清理浏览器进程。")

        if force_process_cleanup:
            cleaned = self._terminate_managed_browser_processes(driver_to_close)
            if cleaned:
                self.log(f"已清理 {cleaned} 个浏览器相关残留进程。")

        self._cleanup_temp_user_data_dir()
        gc.collect()

    def _load_session_after_restart(self):
        """重启浏览器后尽量恢复Trip登录态。"""
        try:
            self.driver.get("https://www.trip.com/")
            if self.session_manager.load_session(self.driver):
                self.driver.refresh()
                self.log("✓ 浏览器重启后已恢复会话。")
            else:
                self.log("⚠ 浏览器重启后未找到可恢复的会话。")
        except Exception as e:
            self.log(f"⚠ 浏览器重启后恢复会话失败: {e}")

    def _restart_browser_for_memory_guard(self, reason):
        if self.is_stopped.is_set():
            return None

        self._browser_restart_count += 1
        self.log(
            f"触发浏览器内存保护重启（第 {self._browser_restart_count} 次）：{reason}。"
        )

        try:
            if self.driver:
                self.session_manager.save_session(self.driver)
        except Exception as e:
            self.log(f"重启前保存会话失败（继续重启）: {e}")

        with self.browser_lock:
            self.browser_state = "CLOSING"

        self._shutdown_driver(stop_screen_prevention=False, force_process_cleanup=True)
        time.sleep(1)

        with self.browser_lock:
            self.browser_state = "OPENING"

        self._initialize_driver_for_current_os()
        self._configure_driver_after_restart()
        self._load_session_after_restart()
        self._processed_since_browser_restart = 0

        with self.browser_lock:
            self.browser_state = "OPEN"

        self.log("浏览器内存保护重启完成，继续处理后续URL。")
        try:
            return self.driver.current_window_handle
        except:
            return None

    def _maybe_restart_browser_for_memory_guard(self):
        if not self.browser_memory_guard_enabled or not self.driver or self.is_stopped.is_set():
            return None

        self._processed_since_browser_restart += 1
        rss_mb = self._managed_browser_rss_mb()
        reason = None

        if rss_mb is not None:
            self.log(
                f"浏览器内存统计：{rss_mb:.0f}MB，当前批次已处理 "
                f"{self._processed_since_browser_restart}/{self.browser_restart_every} 条。"
            )
            if self.browser_memory_limit_mb > 0 and rss_mb >= self.browser_memory_limit_mb:
                reason = f"Chrome内存 {rss_mb:.0f}MB 已超过 {self.browser_memory_limit_mb}MB"
        else:
            self.log(
                f"浏览器内存统计不可用，当前批次已处理 "
                f"{self._processed_since_browser_restart}/{self.browser_restart_every} 条。"
            )

        if (
            not reason
            and self.browser_restart_every > 0
            and self._processed_since_browser_restart >= self.browser_restart_every
        ):
            reason = f"已连续处理 {self._processed_since_browser_restart} 条URL"

        if reason:
            try:
                return self._restart_browser_for_memory_guard(reason)
            except Exception as e:
                self.log(f"浏览器内存保护重启失败，任务将停止: {e}")
                self.is_stopped.set()
                return None
        return None

    def _initialize_chrome_browser_windows(self):
        """Windows系统初始化Chrome浏览器 - 优先使用undetected-chromedriver"""
        # 获取Chrome版本
        chrome_version = self._get_installed_chrome_version_windows()
        if chrome_version:
            version_main = int(chrome_version.split('.')[0])
            self.log(f"检测到Chrome版本: {chrome_version} (主版本: {version_main})")
        else:
            version_main = None
            self.log("无法检测Chrome版本，将使用默认版本")
        
        # 创建临时用户数据目录（支持多开）
        user_data_dir = self._create_temp_user_data_dir()

        # 方法1：尝试使用undetected-chromedriver（指定版本）
        try:
            self.log("方法1：正在使用undetected-chromedriver启动Chrome...")

            options = uc.ChromeOptions()
            options.add_argument("--start-maximized")
            options.add_argument("--no-sandbox")
            options.add_argument("--disable-dev-shm-usage")
            options.add_argument(f"--user-data-dir={user_data_dir}")
            self._add_memory_optimization_args(options)
            self._apply_cli_browser_options(options)

            self.log(f"启动undetected Chrome浏览器 (version_main={version_main})...")
            self.driver = uc.Chrome(options=options, version_main=version_main, use_subprocess=True)
            
            self.log("✓ undetected Chrome浏览器启动成功！")
            return
            
        except Exception as e:
            self.log(f"✗ undetected-chromedriver启动失败: {e}")
        
        # 方法2：尝试使用undetected-chromedriver的无选项模式
        try:
            self.log("方法2：尝试undetected-chromedriver无选项模式...")

            options = uc.ChromeOptions()
            options.add_argument(f"--user-data-dir={user_data_dir}")
            self._add_memory_optimization_args(options)
            self._apply_cli_browser_options(options)

            self.driver = uc.Chrome(options=options)
            if not getattr(self, "cli_headless", False):
                self.driver.maximize_window()
            
            self.log("✓ undetected Chrome（无选项模式）启动成功！")
            return
            
        except Exception as e:
            self.log(f"✗ undetected-chromedriver无选项模式失败: {e}")
        
        # 方法3：使用传统WebDriver作为最后备用
        try:
            self.log("方法3：使用传统WebDriver作为备用方案...")
            
            driver_path = self._get_chrome_driver_path()
            if driver_path:
                self.log(f"使用ChromeDriver路径: {driver_path}")
                service = ChromeService(executable_path=driver_path)
                options = ChromeOptions()

                options.add_argument("--start-maximized")
                options.add_argument("--no-sandbox")
                options.add_argument("--disable-dev-shm-usage")
                options.add_argument("--disable-blink-features=AutomationControlled")
                options.add_argument(f"--user-data-dir={user_data_dir}")
                self._add_memory_optimization_args(options)
                self._apply_cli_browser_options(options)

                options.add_experimental_option("excludeSwitches", ["enable-automation", "enable-logging"])
                options.add_experimental_option('useAutomationExtension', False)

                self.driver = webdriver.Chrome(service=service, options=options)
                self.driver.execute_script("Object.defineProperty(navigator, 'webdriver', {get: () => undefined})")
                
                self.log("✓ 传统WebDriver启动成功！")
                return
            else:
                raise Exception("无法找到ChromeDriver")
                
        except Exception as e2:
            self.log(f"✗ 传统WebDriver也启动失败: {e2}")
            raise Exception(f"所有Chrome启动方式都失败了。请检查Chrome浏览器是否正确安装。")
    
    def _initialize_chrome_browser_mac(self):
        """macOS系统初始化Chrome浏览器 - 多策略回退机制"""
        # 创建临时用户数据目录（支持多开）
        user_data_dir = self._create_temp_user_data_dir()

        # 策略1：尝试使用undetected-chromedriver（推荐）
        try:
            self.log("策略1：尝试使用undetected-chromedriver...")

            # 修复SSL证书问题：设置环境变量禁用SSL验证（仅用于undetected-chromedriver下载）
            original_ssl_context = os.environ.get('SSL_CERT_FILE')
            try:
                # 临时禁用SSL验证（仅用于下载驱动）
                import ssl
                ssl._create_default_https_context = ssl._create_unverified_context
            except:
                pass

            options = uc.ChromeOptions()
            options.add_argument("--start-maximized")
            options.add_argument("--no-sandbox")
            options.add_argument("--disable-dev-shm-usage")
            options.add_argument("--disable-blink-features=AutomationControlled")
            options.add_argument(f"--user-data-dir={user_data_dir}")
            self._add_memory_optimization_args(options)
            self._apply_cli_browser_options(options)

            # 尝试启动
            self.driver = uc.Chrome(options=options, version_main=None, driver_executable_path=None)
            self.log("✓ undetected Chrome浏览器启动成功！")
            
            # 恢复SSL上下文
            if original_ssl_context:
                os.environ['SSL_CERT_FILE'] = original_ssl_context
            return
            
        except Exception as e:
            error_msg = str(e)
            self.log(f"✗ undetected-chromedriver失败: {error_msg}")
            
            # 如果是SSL错误，尝试使用预安装的ChromeDriver
            if 'SSL' in error_msg or 'certificate' in error_msg.lower():
                self.log("检测到SSL证书问题，将尝试使用传统WebDriver...")
        
        # 策略2：使用传统WebDriver + ChromeDriver自动下载和安装
        try:
            self.log("策略2：使用传统WebDriver...")
            
            # 确保ChromeDriver已安装或下载
            driver_path = self._ensure_chromedriver_installed()
            if not driver_path:
                raise Exception("无法获取或安装ChromeDriver")
            
            self.log(f"使用ChromeDriver路径: {driver_path}")
            
            # 检查Chrome浏览器
            chrome_path = self._get_chrome_binary_path()
            if not chrome_path:
                self.log("未找到Chrome浏览器")
                raise Exception("未找到Chrome浏览器。请确保已安装Google Chrome浏览器。")
            
            self.log(f"找到Chrome浏览器: {chrome_path}")
            
            # 创建Chrome服务
            service = ChromeService(executable_path=driver_path)
            
            # 配置Chrome选项
            options = ChromeOptions()
            options.binary_location = chrome_path
            options.add_argument("--start-maximized")
            options.add_argument("--disable-blink-features=AutomationControlled")
            options.add_argument("--disable-dev-shm-usage")
            options.add_argument("--no-sandbox")
            options.add_argument("--disable-gpu")
            options.add_argument("--disable-ime")
            options.add_argument("--disable-extensions")
            options.add_argument(f"--user-data-dir={user_data_dir}")
            self._add_memory_optimization_args(options)
            self._apply_cli_browser_options(options)

            options.add_experimental_option("excludeSwitches", ["enable-automation", "enable-logging"])
            options.add_experimental_option('useAutomationExtension', False)

            # 创建Chrome实例
            self.driver = webdriver.Chrome(service=service, options=options)
            self.driver.execute_script("Object.defineProperty(navigator, 'webdriver', {get: () => undefined})")
            
            self.log("✓ Chrome浏览器（传统WebDriver）启动成功！")
            return
            
        except Exception as e:
            self.log(f"✗ 传统WebDriver失败: {e}")
            raise Exception(f"无法启动Chrome浏览器。错误: {e}\n\n请检查：\n1. 是否已安装Google Chrome浏览器\n2. 网络连接是否正常\n3. 系统权限是否足够")
    
    def _ensure_chromedriver_installed(self):
        """确保ChromeDriver已正确安装并可用（macOS专用）"""
        # 获取Chrome版本
        chrome_version = self._get_chrome_version()
        if not chrome_version:
            self.log("无法检测Chrome版本")
            return None
        
        chrome_major = chrome_version.split('.')[0]
        
        # 首先检查已安装的ChromeDriver（必须版本匹配）
        driver_path = self._find_existing_chromedriver(chrome_major)
        if driver_path:
            return driver_path
        
        # 如果没有找到匹配版本，则下载并安装
        self.log(f"未找到匹配Chrome {chrome_major}的ChromeDriver，开始自动下载安装...")
        return self._download_and_install_chromedriver()
    
    def _find_existing_chromedriver(self, required_major_version=None):
        """查找系统中已存在且版本匹配的ChromeDriver"""
        local_driver_paths = [
            "/usr/local/bin/chromedriver",
            "/opt/homebrew/bin/chromedriver",
            "/usr/bin/chromedriver",
            os.path.expanduser("~/bin/chromedriver"),
        ]
        
        # 搜索缓存目录下的所有版本
        cache_base = os.path.join(os.path.expanduser('~'), '.chromedriver_cache', "mac64")
        if os.path.exists(cache_base):
            for version_dir in os.listdir(cache_base):
                cache_path = os.path.join(cache_base, version_dir, "chromedriver")
                if cache_path not in local_driver_paths:
                    local_driver_paths.append(cache_path)
        
        for path in local_driver_paths:
            # 如果是目录，查找其中的chromedriver
            if os.path.isdir(path):
                potential_path = os.path.join(path, "chromedriver")
                if os.path.exists(potential_path):
                    path = potential_path
                else:
                    continue
            
            if os.path.exists(path):
                # 验证可执行性并检查版本
                driver_version = self._get_chromedriver_version(path)
                if driver_version:
                    driver_major = driver_version.split('.')[0]
                    
                    # 如果需要匹配版本
                    if required_major_version:
                        if driver_major == required_major_version:
                            self.log(f"找到版本匹配的ChromeDriver: {path} (版本 {driver_version})")
                            return path
                        else:
                            self.log(f"跳过版本不匹配的ChromeDriver: {path} (版本 {driver_version}，需要 {required_major_version})")
                            continue
                    else:
                        # 不需要版本匹配，只要可执行即可
                        if self._verify_chromedriver_executable(path):
                            self.log(f"找到可用的ChromeDriver: {path}")
                            return path
        
        return None
    
    def _get_chromedriver_version(self, driver_path):
        """获取ChromeDriver版本号"""
        try:
            if not os.path.exists(driver_path):
                return None
            
            result = subprocess.run([driver_path, "--version"], 
                                  capture_output=True, text=True, timeout=10)
            if result.returncode == 0:
                # 解析版本号：ChromeDriver 142.0.7444.177 (...)
                import re
                match = re.search(r'ChromeDriver (\d+\.\d+\.\d+\.\d+)', result.stdout)
                if match:
                    return match.group(1)
            return None
        except Exception as e:
            return None
    
    def _get_chrome_driver_path(self):
        """查找或下载匹配当前系统Chrome版本的WebDriver（兼容旧接口）"""
        if self.current_os == "Darwin":
            return self._ensure_chromedriver_installed()
        else:
            # Windows逻辑保持不变
            try:
                # 检查本地是否已有ChromeDriver
                if getattr(sys, 'frozen', False):
                    script_dir = os.path.dirname(sys.executable)
                else:
                    script_dir = os.path.dirname(os.path.abspath(__file__))
                local_driver_paths = [os.path.join(script_dir, "chromedriver.exe")]
                
                for path in local_driver_paths:
                    if os.path.exists(path):
                        try:
                            result = subprocess.run([path, "--version"], 
                                                  capture_output=True, text=True, timeout=5)
                            if result.returncode == 0:
                                self.log(f"使用本地ChromeDriver: {path}")
                                return path
                        except Exception:
                            continue
                
                # 下载驱动
                chrome_version = self._get_chrome_version()
                if not chrome_version:
                    self.log("无法检测Chrome版本")
                    return None
                
                major_version = chrome_version.split('.')[0]
                self.log(f"检测到Chrome版本: {chrome_version}")
                
                cache_dir = script_dir
                self.log(f"正在下载ChromeDriver for Chrome {major_version}...")
                driver_path = self._download_chromedriver(chrome_version, cache_dir)
                return driver_path
                
            except Exception as e:
                self.log(f"获取ChromeDriver失败: {e}")
                return None
    
    def _download_and_install_chromedriver(self):
        """下载并安装ChromeDriver（macOS专用）"""
        chrome_version = self._get_chrome_version()
        if not chrome_version:
            raise Exception("无法检测Chrome版本，请确保已安装Google Chrome浏览器")
        
        major_version = chrome_version.split('.')[0]
        self.log(f"检测到Chrome版本: {chrome_version} (主版本: {major_version})")
        
        # 设置缓存目录
        cache_dir = os.path.join(os.path.expanduser('~'), '.chromedriver_cache', "mac64")
        os.makedirs(cache_dir, exist_ok=True)
        
        # 下载驱动
        driver_path = self._download_chromedriver(chrome_version, cache_dir)
        
        if driver_path and self._verify_chromedriver_executable(driver_path):
            return driver_path
        else:
            raise Exception("ChromeDriver下载或验证失败")
    
    def _download_chromedriver(self, chrome_version, cache_dir):
        """下载匹配Chrome版本的ChromeDriver"""
        try:
            major_version = chrome_version.split('.')[0]
            
            # 新的ChromeDriver下载地址 (Chrome 115+)
            if int(major_version) >= 115:
                # 获取所有可用版本
                base_url = "https://googlechromelabs.github.io/chrome-for-testing"
                versions_url = f"{base_url}/known-good-versions-with-downloads.json"
                
                self.log(f"正在查找匹配Chrome {major_version}的ChromeDriver...")
                response = self._robust_http_get(versions_url, max_retries=5, timeout=30)
                
                if response and response.status_code == 200:
                    data = response.json()
                    versions = data.get('versions', [])
                    
                    # 查找匹配当前Chrome主版本的最新ChromeDriver
                    matching_version = None
                    matching_downloads = None
                    
                    # 从后往前遍历（新版本在后面）
                    for version_info in reversed(versions):
                        version_str = version_info.get('version', '')
                        if version_str.startswith(f"{major_version}."):
                            # 检查是否有chromedriver下载
                            downloads = version_info.get('downloads', {}).get('chromedriver', [])
                            if downloads:
                                matching_version = version_str
                                matching_downloads = downloads
                                break
                    
                    if not matching_version:
                        self.log(f"未找到匹配Chrome {major_version}的ChromeDriver，尝试使用最新稳定版...")
                        # 回退到最新稳定版
                        latest_url = f"{base_url}/last-known-good-versions-with-downloads.json"
                        response = self._robust_http_get(latest_url, max_retries=5, timeout=30)
                        if response and response.status_code == 200:
                            data = response.json()
                            matching_version = data['channels']['Stable']['version']
                            matching_downloads = data['channels']['Stable']['downloads']['chromedriver']
                        else:
                            raise Exception("无法获取ChromeDriver版本信息")
                    
                    driver_version = matching_version
                    self.log(f"选择ChromeDriver版本: {driver_version}")
                    
                    download_url = None
                    system_arch = platform.machine()
                    self.log(f"检测到系统架构: {system_arch}")
                    
                    if self.current_os == "Darwin":
                        for download in matching_downloads:
                            if system_arch == 'arm64' and download['platform'] == 'mac-arm64':
                                download_url = download['url']
                                self.log("选择ARM64版本的ChromeDriver")
                                break
                            elif system_arch == 'x86_64' and download['platform'] == 'mac-x64':
                                download_url = download['url']
                                self.log("选择x86_64版本的ChromeDriver")
                                break
                        
                        if not download_url:
                            for download in matching_downloads:
                                if download['platform'] == 'mac-x64':
                                    download_url = download['url']
                                    self.log("回退到mac-x64版本")
                                    break
                    else:  # Windows
                        for download in matching_downloads:
                            if download['platform'] == 'win64':
                                download_url = download['url']
                                break
                    
                    if not download_url:
                        raise Exception(f"找不到{self.current_os}版本的ChromeDriver")
                else:
                    raise Exception("无法获取ChromeDriver版本信息")
            else:
                # 旧版本Chrome（已废弃）
                raise Exception(f"Chrome版本 {major_version} 过旧，需要Chrome 115或更高版本")
            
            driver_dir = os.path.join(cache_dir, driver_version)
            os.makedirs(driver_dir, exist_ok=True)
            
            if self.current_os == "Darwin":
                driver_path = os.path.join(driver_dir, "chromedriver")
            else:
                driver_path = os.path.join(driver_dir, "chromedriver.exe")
            
            # 如果已存在且可执行，直接返回
            if os.path.exists(driver_path):
                if self._verify_chromedriver_executable(driver_path):
                    self.log(f"使用已存在的ChromeDriver: {driver_path}")
                    return driver_path
                else:
                    # 如果存在但不可执行，删除重新下载
                    try:
                        os.remove(driver_path)
                    except:
                        pass
            
            self.log(f"正在从 {download_url} 下载...")
            response = self._robust_http_get(download_url, max_retries=5, timeout=120, stream=True)
            if not response:
                raise Exception("下载失败")
            response.raise_for_status()
            
            # 解压文件
            zip_content = response.content
            self.log(f"下载完成，文件大小: {len(zip_content) / 1024 / 1024:.2f} MB")
            
            with zipfile.ZipFile(io.BytesIO(zip_content)) as z:
                if int(major_version) >= 115:
                    extracted_path = None
                    # 方法1：尝试查找并提取chromedriver文件
                    for file_info in z.filelist:
                        filename = file_info.filename
                        # 查找名为chromedriver的文件（不区分大小写）
                        base_name = os.path.basename(filename)
                        if (base_name == 'chromedriver' or 
                            (self.current_os == "Windows" and base_name == 'chromedriver.exe')):
                            if not file_info.is_dir():
                                # 提取到临时目录
                                z.extract(file_info, driver_dir)
                                extracted_path = os.path.join(driver_dir, filename)
                                # 如果路径包含子目录，需要找到实际文件
                                if os.path.isfile(extracted_path):
                                    self.log(f"从ZIP中提取文件: {filename}")
                                    break
                                elif os.path.isdir(extracted_path):
                                    # 如果是目录，继续查找
                                    extracted_path = None
                    
                    # 方法2：如果没找到，提取所有文件并搜索
                    if not extracted_path or not os.path.exists(extracted_path):
                        self.log("提取所有ZIP文件...")
                        z.extractall(driver_dir)
                        
                        # 递归搜索chromedriver文件
                        for root, dirs, files in os.walk(driver_dir):
                            for file in files:
                                if file == 'chromedriver' or (self.current_os == "Windows" and file == 'chromedriver.exe'):
                                    candidate_path = os.path.join(root, file)
                                    if os.path.isfile(candidate_path):
                                        extracted_path = candidate_path
                                        self.log(f"在解压文件中找到: {candidate_path}")
                                        break
                            if extracted_path:
                                break
                    
                    if not extracted_path or not os.path.exists(extracted_path):
                        raise Exception("未能从ZIP中提取ChromeDriver文件。请检查ZIP文件是否完整。")
                    
                    # 移动到最终位置（如果不在正确位置）
                    if extracted_path != driver_path:
                        # 确保目标目录存在
                        os.makedirs(os.path.dirname(driver_path), exist_ok=True)
                        if os.path.exists(driver_path):
                            try:
                                os.remove(driver_path)
                            except:
                                pass
                        try:
                            os.rename(extracted_path, driver_path)
                            self.log(f"移动文件到: {driver_path}")
                        except Exception as e:
                            # 如果rename失败，尝试复制
                            shutil.copy2(extracted_path, driver_path)
                            self.log(f"复制文件到: {driver_path} (原因: {e})")
            
            # macOS特殊处理：移除quarantine属性并设置权限
            if os.path.exists(driver_path) and self.current_os == "Darwin":
                # 移除quarantine属性（macOS安全机制）
                try:
                    subprocess.run(['xattr', '-d', 'com.apple.quarantine', driver_path], 
                                 check=False, timeout=5, capture_output=True)
                except:
                    pass
                
                # 设置执行权限
                os.chmod(driver_path, 0o755)
                
                self.log(f"ChromeDriver下载成功: {driver_path}")
                
                # 验证可执行性
                if not self._verify_chromedriver_executable(driver_path):
                    raise Exception("ChromeDriver验证失败")
                
                return driver_path
            elif os.path.exists(driver_path):
                # Windows
                self.log(f"ChromeDriver下载成功: {driver_path}")
                return driver_path
            else:
                raise Exception(f"ChromeDriver未正确解压")
            
        except Exception as e:
            self.log(f"下载ChromeDriver失败: {e}")
            import traceback
            self.log(f"详细错误: {traceback.format_exc()}")
            return None
    
    def _robust_http_get(self, url, max_retries=5, timeout=30, stream=False):
        """稳健的HTTP请求方法，处理SSL错误"""
        headers = {
            'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36'
        }
        
        for attempt in range(max_retries):
            try:
                if attempt == 0:
                    response = requests.get(url, timeout=timeout, headers=headers, stream=stream, verify=True)
                    response.raise_for_status()
                    return response
                else:
                    self.log(f"HTTP请求重试 {attempt+1}/{max_retries} (禁用SSL验证)")
                    response = requests.get(url, timeout=timeout, headers=headers, stream=stream, verify=False)
                    response.raise_for_status()
                    return response
                    
            except requests.exceptions.SSLError as e:
                self.log(f"SSL错误: {str(e)[:100]}")
            except requests.exceptions.Timeout as e:
                self.log(f"超时错误: {str(e)[:100]}")
            except requests.exceptions.ConnectionError as e:
                self.log(f"连接错误: {str(e)[:100]}")
            except Exception as e:
                self.log(f"其他错误: {str(e)[:100]}")
            
            if attempt < max_retries - 1:
                wait_time = min(2 ** attempt, 10)
                self.log(f"等待 {wait_time} 秒后重试...")
                time.sleep(wait_time)
        
        return None
    
    def _get_chrome_version(self):
        """获取Chrome版本"""
        if self.current_os == "Darwin":  # macOS
            return self._get_installed_chrome_version_mac()
        else:  # Windows
            return self._get_installed_chrome_version_windows()
    
    def _get_installed_chrome_version_windows(self):
        """获取Windows上Chrome版本"""
        try:
            cmd = r'reg query "HKEY_CURRENT_USER\Software\Google\Chrome\BLBeacon" /v version'
            output = subprocess.check_output(cmd, shell=True, text=True, stderr=subprocess.DEVNULL)
            version = re.search(r'version\s+REG_SZ\s+([^\s]+)', output).group(1)
            return version
        except:
            try:
                chrome_path = r"C:\Program Files\Google\Chrome\Application\chrome.exe"
                if os.path.exists(chrome_path):
                    cmd = f'powershell "(Get-ItemProperty \'{chrome_path}\').VersionInfo.FileVersion"'
                    output = subprocess.check_output(cmd, shell=True, text=True).strip()
                    return output
            except:
                pass
        return None
    
    def _get_installed_chrome_version_mac(self):
        """获取macOS上Chrome版本"""
        try:
            chrome_path = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
            if os.path.exists(chrome_path):
                cmd = f'"{chrome_path}" --version'
                result = subprocess.check_output(cmd, shell=True, text=True, stderr=subprocess.DEVNULL).strip()
                version_match = re.search(r'Google Chrome (\d+\.\d+\.\d+\.\d+)', result)
                if version_match:
                    return version_match.group(1)
            return None
        except Exception as e:
            self.log(f"获取Chrome版本失败: {e}")
            return None
    
    def _verify_chromedriver_executable(self, driver_path):
        """验证ChromeDriver是否可执行（macOS增强版）"""
        try:
            if not os.path.exists(driver_path):
                return False
            
            # macOS特殊处理
            if self.current_os == "Darwin":
                # 1. 移除quarantine属性（如果存在）
                try:
                    result = subprocess.run(['xattr', '-l', driver_path], 
                                          capture_output=True, text=True, timeout=5)
                    if 'com.apple.quarantine' in result.stdout:
                        self.log("检测到quarantine属性，正在移除...")
                        subprocess.run(['xattr', '-d', 'com.apple.quarantine', driver_path], 
                                     check=False, timeout=5, capture_output=True)
                except:
                    pass
                
                # 2. 确保有执行权限
                if not os.access(driver_path, os.X_OK):
                    try:
                        os.chmod(driver_path, 0o755)
                        self.log("已添加执行权限")
                    except Exception as e:
                        self.log(f"无法添加执行权限: {e}")
                        return False
                
                # 3. 检查文件是否为可执行文件
                try:
                    import stat
                    file_stat = os.stat(driver_path)
                    if not (file_stat.st_mode & stat.S_IEXEC):
                        os.chmod(driver_path, file_stat.st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
                except:
                    pass
            
            # 4. 实际执行测试
            try:
                result = subprocess.run([driver_path, "--version"], 
                                      capture_output=True, text=True, timeout=15)
                if result.returncode == 0 and 'ChromeDriver' in result.stdout:
                    version_info = result.stdout.strip().split('\n')[0]
                    self.log(f"ChromeDriver验证成功: {version_info}")
                    return True
                else:
                    self.log(f"ChromeDriver版本检查失败: returncode={result.returncode}")
                    return False
            except subprocess.TimeoutExpired:
                self.log("ChromeDriver验证超时")
                return False
            except FileNotFoundError:
                self.log(f"ChromeDriver文件不存在: {driver_path}")
                return False
            except PermissionError:
                self.log(f"ChromeDriver权限不足: {driver_path}")
                return False
            except Exception as e:
                error_msg = str(e)
                if 'Exec format error' in error_msg:
                    self.log(f"ChromeDriver架构不匹配: {error_msg}")
                else:
                    self.log(f"ChromeDriver执行错误: {error_msg}")
                return False
                
        except Exception as e:
            self.log(f"验证ChromeDriver时发生异常: {e}")
            import traceback
            self.log(f"详细错误: {traceback.format_exc()}")
            return False
    
    def _get_chrome_binary_path(self):
        """获取Chrome浏览器路径（macOS）"""
        if self.current_os != "Darwin":
            return None
        
        try:
            chrome_paths = [
                "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
                "/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
                "/Applications/Chromium.app/Contents/MacOS/Chromium"
            ]
            
            for chrome_path in chrome_paths:
                if os.path.exists(chrome_path):
                    return chrome_path
            
            return None
            
        except Exception as e:
            self.log(f"查找Chrome路径失败: {e}")
            return None

    def _browser_initialized_callback(self, success, message):
        """浏览器初始化完成后的回调函数"""
        if success:
            self.log(message)
            
            # 自动尝试加载会话数据（用于快速登录）
            self.log("=" * 50)
            self.log("正在尝试加载保存的登录会话数据...")
            try:
                if self.driver and self.session_manager.load_session(self.driver):
                    self.log("✓ 会话数据加载成功！已应用登录状态。")
                    # 刷新页面以应用加载的cookies
                    self.driver.refresh()
                    self.log("已刷新页面以应用会话")
                else:
                    self.log("✗ 未找到保存的会话数据，需要手动登录")
            except Exception as e:
                self.log(f"⚠ 加载会话数据时出错: {e}")
            
            self.log("=" * 50)
            self.btn_open_browser.config(state=tk.NORMAL, text="打开浏览器")
        else:
            self.log(f"浏览器启动失败: {message}")
            self.btn_open_browser.config(state=tk.NORMAL, text="打开浏览器")
            messagebox.showerror("浏览器启动失败", f"无法启动Chrome浏览器。\n\n错误详情: {message}\n\n请确保已安装Chrome浏览器，并尝试以管理员权限运行程序。")
    
    def _safe_driver_quit(self):
        """安全关闭浏览器 - 带超时和多重尝试"""
        if not self.driver:
            self.log("浏览器实例不存在。")
            self._shutdown_driver(stop_screen_prevention=True, force_process_cleanup=True)
            with self.browser_lock:
                self.browser_state = "CLOSED"
            return
        
        self.log("正在关闭浏览器...")
        try:
            self._shutdown_driver(stop_screen_prevention=True, force_process_cleanup=True)
        except Exception as e:
            self.log(f"关闭浏览器时出错（可忽略）: {e}")
        finally:
            with self.browser_lock:
                self.browser_state = "CLOSED"

    def close_browser(self):
        """关闭浏览器 - 稳健版本"""
        with self.browser_lock:
            if self.browser_state == "CLOSED":
                self.log("浏览器尚未打开。")
                return
            elif self.browser_state == "CLOSING":
                self.log("浏览器正在关闭中，请稍候...")
                return
            elif self.browser_state == "OPENING":
                self.log("浏览器正在启动中，请稍候后再试...")
                return
            self.browser_state = "CLOSING"
        
        # 禁用关闭按钮防止重复点击
        self.btn_close_browser.config(state=tk.DISABLED, text="关闭中...")
        
        def close_thread_func():
            try:
                # 先停止任何正在进行的抓取任务
                self.is_stopped.set()
                self.is_paused.set()
                
                # 关闭浏览器
                self._safe_driver_quit()
            except Exception as e:
                self.log(f"关闭浏览器线程出错: {e}")
            finally:
                # 恢复按钮状态
                self.root.after(0, lambda: self.btn_close_browser.config(state=tk.NORMAL, text="关闭浏览器"))
                self.root.after(0, lambda: self.update_button_states(False))
        
        # 在后台线程中关闭浏览器，避免GUI卡死
        threading.Thread(target=close_thread_func, daemon=True).start()
    
    def update_button_states(self, is_running):
        state = tk.NORMAL if not is_running else tk.DISABLED
        self.btn_start.config(state=state)
        self.btn_open_browser.config(state=state)
        self.btn_pause_resume.config(state=tk.DISABLED if not is_running else tk.NORMAL, text="暂停执行")
        self.btn_stop.config(state=tk.DISABLED if not is_running else tk.NORMAL)
        
        # 导出按钮：始终可用
        self.btn_export.config(state=tk.NORMAL)
        
        # 清空按钮：有数据时启用
        if len(self.results_data) > 0:
            self.btn_clear.config(state=tk.NORMAL)
        else:
            self.btn_clear.config(state=tk.DISABLED)
        
        if not is_running: 
            self.is_paused.set()
    
    def manual_export(self):
        """手动导出到Excel - 在后台线程执行避免GUI卡死"""
        if not self.results_data:
            messagebox.showwarning("提示", "没有数据可导出。")
            return
        
        # 禁用导出按钮防止重复点击
        self.btn_export.config(state=tk.DISABLED, text="导出中...")
        self.log(f"正在导出 {len(self.results_data)} 条记录...")
        
        # 在后台线程中执行导出
        def export_thread():
            try:
                self._save_to_xlsx(self.results_data)
            except Exception as e:
                self.log(f"导出失败: {e}")
                self.root.after(0, lambda: messagebox.showerror("导出失败", f"导出Excel时发生错误：{e}"))
            finally:
                # 恢复按钮状态
                self.root.after(0, lambda: self.btn_export.config(state=tk.NORMAL, text="导出到Excel"))
        
        threading.Thread(target=export_thread, daemon=True).start()

    def start_scraping(self):
        if self.excel_input_path:
            self.start_excel_scraping()
            return
        if not self.driver: 
            messagebox.showwarning("提示", "请先点击 '打开浏览器' 按钮。")
            return
        if self.scraping_thread and self.scraping_thread.is_alive(): 
            self.log("任务已在进行中。")
            return
        urls = self.url_text.get("1.0", tk.END).strip().split('\n')
        self.urls_to_process = [url for url in urls if url.strip()]
        if not self.urls_to_process: 
            messagebox.showwarning("提示", "请输入至少一个网址。")
            return
        self.is_stopped.clear()
        self.is_paused.set()
        
        # 初始化进度显示
        self.update_progress(0, len(self.urls_to_process))
        
        self.scraping_thread = threading.Thread(target=self._scraping_logic, daemon=True)
        self.scraping_thread.start()
        self.update_button_states(is_running=True)
        self.log(f"任务开始，共 {len(self.urls_to_process)} 个网址待处理。")

    def start_excel_scraping(self):
        if self.scraping_thread and self.scraping_thread.is_alive():
            self.log("任务已在进行中。")
            return
        try:
            workers = int(self.vendor_workers_var.get())
        except Exception:
            workers = 5
        workers = max(1, min(5, workers))
        self.vendor_workers_var.set(workers)
        self.is_stopped.clear()
        self.is_paused.set()
        self.include_vendor_column = True
        self.results_data = []
        self._setup_treeview_columns()
        self.update_progress(0, 0)
        self.update_button_states(is_running=True)
        self.log(f"Excel厂商并发任务开始，并发浏览器数: {workers}")

        def run_batch():
            try:
                from models import BrowserOptions, TaskRequest
                from service import scrape_vendor_batches

                request = TaskRequest(
                    excel_input=self.excel_input_path,
                    workers=workers,
                    rating_only=self.rating_only_var.get(),
                    extract_real_price=self.extract_real_price_var.get(),
                    extract_product_info=self.extract_product_info_var.get(),
                    browser_options=BrowserOptions(
                        memory_guard_enabled=self.browser_memory_guard_enabled,
                        memory_limit_mb=self.browser_memory_limit_mb,
                        restart_every=self.browser_restart_every,
                    ),
                )

                def reporter(message):
                    self.log(message)

                def progress(current, total):
                    self.update_progress(current, total)

                def rows_callback(rows):
                    self.results_data.extend(rows)
                    self.root.after(0, self._setup_treeview_columns)
                    for row in rows:
                        self._add_result_to_treeview(row)

                result = scrape_vendor_batches(
                    request,
                    reporter,
                    output_base_dir=str(Path.home() / "Downloads"),
                    should_stop=lambda: self.is_stopped.is_set(),
                    progress_callback=progress,
                    rows_callback=rows_callback,
                )
                if result.success:
                    self.log(f"Excel厂商任务完成，共抓取 {result.processed_count} 行，输出 {len(result.output_paths)} 个文件。")
                    for path in result.output_paths:
                        self.log(f"输出文件: {path}")
                else:
                    self.log(f"Excel厂商任务结束：{result.error}")
                    if result.output_paths:
                        for path in result.output_paths:
                            self.log(f"已输出文件: {path}")
            except Exception as e:
                self.log(f"Excel厂商任务失败: {e}")
            finally:
                self.root.after(0, self.update_button_states, False)

        self.scraping_thread = threading.Thread(target=run_batch, daemon=True)
        self.scraping_thread.start()

    def pause_resume_scraping(self):
        if self.is_paused.is_set():
            self.is_paused.clear()
            self.btn_pause_resume.config(text="恢复执行")
            self.log("任务已暂停。")
        else:
            self.is_paused.set()
            self.btn_pause_resume.config(text="暂停执行")
            self.log("任务已恢复。")

    def stop_scraping(self):
        """停止抓取任务 - 强制停止机制"""
        self.log("正在请求停止任务...")
        
        # 设置停止信号
        self.is_stopped.set()
        self.is_paused.set()  # 确保不会卡在暂停状态
        
        # 禁用停止按钮防止重复点击
        self.btn_stop.config(state=tk.DISABLED, text="停止中...")
        
        def force_stop():
            try:
                # 尝试向浏览器发送停止命令
                if self.driver:
                    try:
                        self.driver.execute_script("window.stop();")
                        self.log("已向浏览器发送停止命令。")
                    except:
                        pass  # 忽略错误
                
                # 等待抓取线程结束（最多3秒）
                if self.scraping_thread and self.scraping_thread.is_alive():
                    self.scraping_thread.join(timeout=3)
                    if self.scraping_thread.is_alive():
                        self.log("抓取线程未能在3秒内停止，将强制继续。")
                    else:
                        self.log("抓取任务已成功停止。")
                else:
                    self.log("没有正在运行的抓取任务。")
                    
            except Exception as e:
                self.log(f"停止任务时出错: {e}")
            finally:
                # 在主线程中更新按钮状态
                self.root.after(0, lambda: self.update_button_states(False))
                self.root.after(0, lambda: self.btn_stop.config(text="停止执行"))
        
        # 在后台线程执行停止操作
        threading.Thread(target=force_stop, daemon=True).start()

    def load_by_timed_scrolling(self, duration=2):
        """
        简化版懒加载对抗策略：
        等待3秒后，向下滚动到底部，间隔1秒尝试4次，然后直接开始任务
        """
        if self.is_stopped.is_set():
            return
        
        try:
            self.log("开始执行页面滚动加载...")
            
            # 滚动前延迟3秒，等待页面初始加载
            self.log("等待3秒让页面初始加载...")
            time.sleep(3.0)
            
            if self.is_stopped.is_set():
                return
            
            # 以1秒为间隔向下滚动到底部，共4次
            scroll_count = 4
            for i in range(scroll_count):
                if self.is_stopped.is_set():
                    return
                
                # 滚动到页面底部
                self.driver.execute_script("window.scrollTo(0, document.body.scrollHeight);")
                self.log(f"第{i+1}次滚动到底部完成")
                
                # 每次滚动后等待1秒（最后一次也等待，确保页面加载完成）
                time.sleep(1)
            
            self.log("页面滚动加载完成，开始抓取数据")
            
        except Exception as e:
            self.log(f"页面滚动加载出错: {e}")
    
    def _wait_for_page_load_complete(self, timeout=3):
        """等待页面JS动态加载完成"""
        try:
            # 等待关键元素出现，表示页面加载完成
            # 检查产品标题是否已加载
            WebDriverWait(self.driver, timeout).until(
                EC.presence_of_element_located((By.CSS_SELECTOR, 'h1[class*="title"]'))
            )
            self.log("页面动态加载完成")
            return True
        except TimeoutException:
            self.log("等待页面加载超时，继续执行")
            return False
        except Exception as e:
            self.log(f"检查页面加载状态时出错: {e}")
            return False

    def _normalize_scrape_task(self, task):
        if isinstance(task, str):
            return task, None
        if isinstance(task, dict):
            return task.get('url', ''), task.get('vendor')
        return getattr(task, 'url', str(task)), getattr(task, 'vendor', None)

    def _scraping_logic(self):
        self.results_data = []  # 重置结果数据
        original_window = self.driver.current_window_handle
        
        for i, task in enumerate(self.urls_to_process):
            url, vendor = self._normalize_scrape_task(task)
            if self.is_stopped.is_set(): 
                self.log("检测到停止信号，正在退出任务循环。")
                break
            
            self.is_paused.wait()
            
            if self.is_stopped.is_set(): 
                self.log("从暂停中恢复后检测到停止信号，退出。")
                break
            
            vendor_label = f" [{vendor}]" if vendor else ""
            self.log(f"--- 开始处理第 {i+1}/{len(self.urls_to_process)} 个网址{vendor_label}: {url} ---")
            
            try:
                self.driver.switch_to.new_window('tab')
                self.driver.get(url)
                
                if self.is_stopped.is_set():
                    break
                
                # 懒加载对抗策略（内含1秒初始延迟）
                self.load_by_timed_scrolling(duration=2)
                
                if self.is_stopped.is_set():
                    break
                    
                current_result_list = self._scrape_single_page(url, vendor)
                self.results_data.extend(current_result_list)
                self.log(f"网址 {url} 数据提取尝试完成，共获取 {len(current_result_list)} 条记录。")
                
                # 更新进度显示（处理完成后）
                self.update_progress(i + 1, len(self.urls_to_process))
                
            except WebDriverException as e:
                if self.is_stopped.is_set():
                    self.log(f"任务被用户停止，已成功中断对 {url} 的处理。")
                else:
                    self.log(f"处理 {url} 时发生浏览器错误: {e}")
                    self.results_data.append(self._create_error_record_for_vendor(url, f'浏览器错误: {e}', vendor))
            except Exception as e:
                self.log(f"处理 {url} 时发生未知严重错误: {e}")
                self.results_data.append(self._create_error_record_for_vendor(url, f'处理错误: {e}', vendor))
            finally:
                if self.driver:
                    try:
                        handles = list(self.driver.window_handles)
                        if len(handles) > 1 and self.driver.current_window_handle != original_window:
                            self.driver.close()
                            self.driver.switch_to.window(original_window)
                            self.log("当前选项卡已关闭。")
                        original_window = self._close_extra_tabs(original_window) or original_window
                    except Exception as e:
                        self.log(f"关闭选项卡时出错: {e}。正在尝试切换回主窗口。")
                        try:
                            original_window = self._close_extra_tabs(original_window) or original_window
                        except:
                            pass

                    # 清理浏览器内存，并按阈值或批次数重启Chrome
                    self._clear_browser_memory()
                    if i + 1 < len(self.urls_to_process):
                        new_original_window = self._maybe_restart_browser_for_memory_guard()
                        if new_original_window:
                            original_window = new_original_window
        
        # 更新最终进度显示
        if not self.is_stopped.is_set():
            self.update_progress(len(self.urls_to_process), len(self.urls_to_process))
            self.log("所有网址处理完毕。")
        else:
            # 如果任务被停止，显示实际处理的数量
            processed_count = i + 1 if 'i' in locals() else 0
            self.update_progress(processed_count, len(self.urls_to_process))
            self.log("任务已停止。")
        
        # ===== 自动导出和保存会话 =====
        if self.results_data:
            # 自动导出 Excel
            self.log("=" * 50)
            self.log("正在自动导出数据到 Excel...")
            try:
                self._save_to_xlsx(self.results_data)
                self.log("✓ Excel 文件已自动保存成功！")
            except Exception as e:
                self.log(f"✗ 自动导出 Excel 失败: {e}")
                self.log(f"已抓取 {len(self.results_data)} 条记录。可手动点击'导出到Excel'按钮重试。")
            
            # 自动保存会话数据
            self.log("=" * 50)
            self.log("正在自动保存登录会话数据...")
            try:
                if self.driver:
                    if self.session_manager.save_session(self.driver):
                        self.log("✓ 会话数据已自动保存成功！下次运行可快速登录。")
                    else:
                        self.log("✗ 保存会话数据时出错")
                else:
                    self.log("⚠ 浏览器未打开，无法保存会话数据")
            except Exception as e:
                self.log(f"✗ 保存会话数据失败: {e}")
            
            self.log("=" * 50)
            self.log(f"任务完成！共抓取并保存 {len(self.results_data)} 条记录。")
        else:
            self.log(f"未抓取到任何数据。")
        
        self.root.after(0, self.update_button_states, False)

    def _extract_product_id(self, url):
        """从URL中提取商品ID - trip.com/things-to-do/detail/后面直到下一个/的纯数字"""
        try:
            import re
            # 匹配 things-to-do/detail/ 后面直到下一个 / 或结尾的数字
            pattern = r'things-to-do/detail/(\d+)'
            match = re.search(pattern, url)
            if match:
                return match.group(1)
            else:
                self.log(f"无法从URL提取商品ID: {url}")
                return "ID提取失败"
        except Exception as e:
            self.log(f"提取商品ID时出错: {e}")
            return "ID提取错误"

    def _create_error_record(self, url, error_msg):
        """创建错误记录"""
        return self._create_error_record_for_vendor(url, error_msg, None)

    def _create_error_record_for_vendor(self, url, error_msg, vendor=None):
        """创建错误记录"""
        product_id = self._extract_product_id(url)
        record = {
            '商品ID': product_id, 
            'URL': url,
            '商品名称': '获取失败',
            '评分': error_msg, 
            '评价数': '', 
            '预定数': '', 
            '客户服务': '', 
            '方便程度': '', 
            '讯号状态': '', 
            '中差评价数': ''
        }
        if getattr(self, "include_vendor_column", False):
            record['厂商'] = vendor or ''
        
        # 添加价格相关列（如果不是仅评分模式）
        if not self.rating_only_var.get():
            record['货币'] = ''
            record['外显价格'] = ''
            if self.extract_real_price_var.get():
                record['实价'] = ''
            record['促销标签'] = ''
        
        # 添加商品资讯列
        if self.extract_product_info_var.get():
            record['商品资讯'] = ''
        
        # 添加空的SKU列
        for i in range(self.max_sku_layers):
            record[f'SKU层级{i+1}'] = '错误' if i == 0 else ''
            
        return record

    def _scrape_single_page(self, url, vendor=None):
        """抓取单个页面的数据"""
        product_id = self._extract_product_id(url)
        
        # 获取基础数据（评分、评价数等）
        base_data = self._get_base_rating_data(product_id, url)
        if getattr(self, "include_vendor_column", False):
            base_data['厂商'] = vendor or ''
        
        # 如果仅抓取评分，直接返回
        if self.rating_only_var.get():
            self.log("【仅抓取评分模式】跳过SKU价格获取")
            self._add_result_to_treeview(base_data)
            return [base_data]
        
        # 提取所有SKU价格组合
        # 注意：此方法会更新 self.max_sku_layers
        old_max_layers = self.max_sku_layers
        sku_price_combinations = self._extract_all_sku_prices()
        
        # 如果SKU层级增加，更新TreeView列
        if self.max_sku_layers > old_max_layers:
            self.log(f"SKU层级增加到 {self.max_sku_layers}，更新表格列...")
            self.root.after(0, self._setup_treeview_columns)
        
        # 如果没有找到SKU组合，尝试获取默认价格作为一条记录
        if not sku_price_combinations:
            default_price_text = self._get_default_price()
            currency, price = self._parse_price(default_price_text)
            
            # 使用基础数据创建记录
            base_data['货币'] = currency
            base_data['外显价格'] = price
            
            if self.extract_real_price_var.get():
                base_data['实价'] = price
                
            base_data['促销标签'] = self._extract_promotional_tags()
            
            # 填充空的SKU列
            for i in range(self.max_sku_layers):
                base_data[f'SKU层级{i+1}'] = '无SKU选项' if i == 0 else ''
                
            self._add_result_to_treeview(base_data)
            return [base_data]
        
        # 将基础评分数据与每个SKU组合合并
        results = []
        for index, sku_combo in enumerate(sku_price_combinations):
            if self.is_stopped.is_set():
                break
                
            result = base_data.copy()
            
            # 只在第一行显示基础信息，其他行留空
            if index > 0:
                # 清空商品ID、商品名称和其他基础信息
                result.update({
                    '厂商': '',
                    '商品ID': '',
                    'URL': '',
                    '商品名称': '',
                    '评分': '',
                    '评价数': '',
                    '预定数': '',
                    '客户服务': '',
                    '方便程度': '',
                    '讯号状态': '',
                    '中差评价数': '',
                    '货币': ''      # 清空货币
                })
                # 商品资讯也只在第一行显示
                if self.extract_product_info_var.get():
                    result['商品资讯'] = ''
            
            # 外显价格（分离货币）
            result['货币'] = sku_combo.get('currency', 'N/A')
            result['外显价格'] = sku_combo.get('price', 'N/A')
            
            # 实价（如果启用）
            if self.extract_real_price_var.get():
                result['实价'] = sku_combo.get('real_price', 'N/A')
            
            # 促销标签（SKU级别）
            result['促销标签'] = sku_combo.get('promo_tag', '')
            
            # 拆分SKU组合到各列
            sku_parts = sku_combo.get('combination', '').split(' | ')
            for i in range(self.max_sku_layers):
                if i < len(sku_parts):
                    result[f'SKU层级{i+1}'] = sku_parts[i]
                else:
                    result[f'SKU层级{i+1}'] = ''
            
            results.append(result)
            
            # 添加到TreeView
            self._add_result_to_treeview(result)
        
        return results

    def _parse_price(self, price_text):
        """解析价格文本，分离货币和数值"""
        import re
        if not price_text or price_text == "价格未找到" or price_text == "未找到":
            return 'N/A', 'N/A'
            
        # 常见货币符号处理
        currency = 'N/A'
        if 'HK$' in price_text:
            currency = 'HKD'
            price_text = price_text.replace('HK$', '').strip()
        elif '$' in price_text:
            currency = 'USD' # 默认为USD，除非有其他标识
            price_text = price_text.replace('$', '').strip()
        elif '¥' in price_text or 'CNY' in price_text:
            currency = 'CNY'
            price_text = price_text.replace('¥', '').replace('CNY', '').strip()
            
        # 提取数字（支持千分位逗号）
        # 匹配数字、小数点和逗号
        match = re.search(r'([0-9,]+\.?[0-9]*)', price_text)
        if match:
            price_value = match.group(1).replace(',', '')
            return currency, price_value
            
        return currency, price_text # Return two values, price_text as value if no numeric match

    def _get_base_rating_data(self, product_id, url):
        """获取基础的评分数据 - 极速版，不等待任何元素"""
        data = {'商品ID': product_id, 'URL': url}
        
        # 商品名称（从页面标题获取，不等待）
        data['商品名称'] = self._get_product_name()
        
        # 评分（立即查找，不等待）
        data['评分'] = self._quick_get_text(By.CSS_SELECTOR, 'span[testid="productdetail-productInfo-commentScore"]')
        
        # 预定数（立即查找，不等待）
        booking_text = self._quick_get_text(By.CSS_SELECTOR, 'span[testid="saleInfoId"]')
        if booking_text and '次預訂' in booking_text:
            data['预定数'] = booking_text.split('次預訂')[0].strip().replace(',', '')
        else:
            data['预定数'] = booking_text.replace(',', '') if booking_text else ''
        
        # 如果没有评分，快速跳过其他评分相关字段
        if not data['评分']:
            data['评价数'] = ''
            data['客户服务'] = ''
            data['方便程度'] = ''
            data['讯号状态'] = ''
            data['中差评价数'] = ''
        else:
            # 有评分，继续获取其他字段（全部立即查找，不等待）
            review_text = self._quick_get_text(By.CSS_SELECTOR, 'span[testid="productdetail-productInfo-commentCountDesc"]')
            if review_text:
                match = re.search(r'\(([0-9,]+)', review_text)
                data['评价数'] = match.group(1).replace(',', '') if match else ''
            else:
                data['评价数'] = ''
            
            data['客户服务'] = self._quick_get_text(By.XPATH, "//div[text()='客戶服務']/following-sibling::span")
            data['方便程度'] = self._quick_get_text(By.XPATH, "//div[text()='方便程度']/following-sibling::span")
            data['讯号状态'] = self._quick_get_text(By.XPATH, "//div[text()='訊號狀態']/following-sibling::span")
            
            bad_review_text = self._quick_get_text(By.XPATH, "//div[contains(@class, 'ct-review-list-filiter-label-item') and contains(., '中性評價及負面評價')]")
            if bad_review_text:
                match = re.search(r'\(([0-9,]+)\)', bad_review_text)
                data['中差评价数'] = match.group(1).replace(',', '') if match else ''
            else:
                data['中差评价数'] = ''
        
        # 商品资讯（如果启用）
        if self.extract_product_info_var.get():
            data['商品资讯'] = self._extract_product_info()
        
        return data
    
    def _quick_get_text(self, by, selector):
        """极速获取元素文本，不等待，找不到立即返回空字符串"""
        try:
            elements = self.driver.find_elements(by, selector)
            if elements:
                text = elements[0].text.strip()
                return text if text else ''
            return ''
        except:
            return ''

    def _get_product_name(self):
        """获取商品名称 - 直接从页面标题获取并去掉后缀"""
        try:
            page_title = self.driver.title
            if page_title:
                # 去掉 " | Trip.com" 和 " - Trip.com" 后缀
                clean_title = page_title.replace(' | Trip.com', '').replace(' - Trip.com', '').strip()
                if clean_title:
                    self.log(f"获取商品名称: {clean_title}")
                    return clean_title
            
            self.log("页面标题为空")
            return "商品名称获取失败"
            
        except Exception as e:
            self.log(f"获取页面标题时出错: {e}")
            return "商品名称获取失败"

    def _get_default_price(self):
        """获取默认显示价格 - 使用验证过的选择器"""
        # 用户提供的选择器结构，尝试泛化ID
        # 原: #group_3814 > ...
        # 尝试使用 class 匹配
        price_selectors = [
            'div[class*="default_new_title_foot_warp"] div[class*="default_new_title_foot_warp_right"] div div div span',
            'div[class*="price-comp_m_footer_price"] span', # 旧的备用
            'span[class*="style_m_price_num"]' # 旧的备用
        ]
        
        for selector in price_selectors:
            try:
                elements = self.driver.find_elements(By.CSS_SELECTOR, selector)
                for element in elements:
                    if element.is_displayed():
                        price_text = element.text.strip()
                        if price_text and ('$' in price_text or 'HK' in price_text or any(char.isdigit() for char in price_text)):
                            self.log(f"找到默认价格: {price_text}")
                            return price_text
            except Exception as e:
                continue
        
        self.log("未找到默认价格")
        return '价格未找到'

    def _extract_all_sku_prices(self):
        """基于真实页面结构提取所有SKU组合和对应价格 - 递归层级遍历"""
        self.log("开始提取SKU选项和价格...")
        
        try:
            # 查找SKU容器 - 基于真实页面结构
            sku_container_selectors = [
                'div[testid="act_sku_wrapper"]',
                'div.style_sku_wrapper_container__Lq9RG',
                'div.style_m_container__ssZE4',
                'div[class*="style_sku_wrapper_container"]'
            ]
            
            sku_container = None
            for selector in sku_container_selectors:
                try:
                    sku_container = WebDriverWait(self.driver, 3).until(
                        EC.presence_of_element_located((By.CSS_SELECTOR, selector))
                    )
                    self.log(f"找到SKU容器: {selector}")
                    break
                except TimeoutException:
                    continue
            
            if not sku_container:
                self.log("未找到SKU容器，可能此产品无SKU选择")
                return []
            
            # 获取所有SKU层级 - 基于真实结构
            sku_layers = self._get_sku_layers_real_structure(sku_container)
            if not sku_layers:
                self.log("未找到SKU层级")
                return []
            
            self.log(f"找到 {len(sku_layers)} 个SKU层级")
            
            # 预处理：获取每层的基础信息
            layer_info = []
            for i, layer in enumerate(sku_layers):
                layer_title = self._get_layer_title(layer)
                layer_info.append({
                    'layer': layer,
                    'title': layer_title,
                    'index': i
                })
                self.log(f"第{i+1}层: '{layer_title}'")
            
            # 更新最大SKU层数
            self.max_sku_layers = max(self.max_sku_layers, len(layer_info))
            
            # 使用递归方式遍历所有层级组合
            self.combinations_result = []
            self.combination_count = 0
            self._recursive_traverse_sku_layers(layer_info, 0, [])
            
            self.log(f"递归遍历完成，共获取 {len(self.combinations_result)} 个SKU价格组合")
            return self.combinations_result
            
        except Exception as e:
            self.log(f"提取SKU选项时发生错误: {e}")
            return []

    def _get_sku_layers_real_structure(self, container):
        """基于真实页面结构获取SKU层级"""
        layer_selectors = [
            'div.sku_tab_ceil.style_m_sku_tab_ceil__D9OYQ',
            'div.xtaro-xview.xt-xview.sku_tab_ceil',
            'div[class*="sku_tab_ceil"]'
        ]
        
        layers = []
        for selector in layer_selectors:
            try:
                elements = container.find_elements(By.CSS_SELECTOR, selector)
                if elements and len(elements) <= 5:  # 最多5层
                    layers = elements[:5]
                    self.log(f"使用选择器 {selector} 找到 {len(layers)} 个层级")
                    break
            except:
                continue
        
        return layers

    def _get_layer_title(self, layer):
        """获取层级标题"""
        try:
            title_element = layer.find_element(By.CSS_SELECTOR, 'span.style_m_tab_title_text__ldPLP')
            return title_element.text.strip()
        except:
            return "未知层级"

    def _get_layer_options_real_structure(self, layer, layer_num):
        """基于真实页面结构获取某层的所有可用选项"""
        options = []
        
        try:
            # 查找选项容器
            options_container = layer.find_element(By.CSS_SELECTOR, 'div.style_m_tab__SI4eq')
            
            # 查找所有选项元素
            option_elements = options_container.find_elements(By.CSS_SELECTOR, 'div.m_ceil.style_m_ceil__mUygk')
            
            for element in option_elements:
                # 检查是否可用 - 基于真实结构
                if self._is_option_available_real_structure(element):
                    # 获取选项文本
                    try:
                        text_element = element.find_element(By.CSS_SELECTOR, 'span[testid^="title_saleproperty_"]')
                        option_text = text_element.text.strip()
                        
                        if option_text:
                            options.append({
                                'element': element,
                                'text': option_text,
                                'layer': layer_num,
                                'id': element.get_attribute('id')
                            })
                            
                    except Exception as e:
                        self.log(f"获取选项文本失败: {e}")
                        continue
        
        except Exception as e:
            self.log(f"获取第{layer_num}层选项时出错: {e}")
        
        return options

    def _is_option_available_real_structure(self, element):
        """基于真实页面结构检查选项是否可用"""
        try:
            # 检查class是否包含 style_select_border__AaoE0 (可点击的标志)
            class_name = element.get_attribute('class') or ''
            if 'style_select_border__AaoE0' not in class_name:
                return False
            
            # 检查文本颜色是否为灰色（不可用）
            try:
                text_element = element.find_element(By.CSS_SELECTOR, 'span[testid^="title_saleproperty_"]')
                text_color = text_element.value_of_css_property('color')
                # 灰色文本表示不可用: rgb(172, 180, 191) 或 rgb(206, 210, 217)
                if 'rgb(172, 180, 191)' in text_color or 'rgb(206, 210, 217)' in text_color:
                    return False
            except:
                pass
            
            # 检查元素的颜色属性
            element_color = element.value_of_css_property('color')
            if 'rgb(206, 210, 217)' in element_color:
                return False
            
            # 检查是否可见和可点击
            if not element.is_displayed() or not element.is_enabled():
                return False
            
            return True
        except Exception as e:
            self.log(f"检查选项可用性时出错: {e}")
            return False

    def _recursive_traverse_sku_layers(self, layer_info, current_layer_index, selected_options):
        """递归遍历SKU层级，添加频繁的停止检查"""
        # 检查停止信号
        if self.is_stopped.is_set():
            self.log("检测到停止信号，中断递归遍历")
            return
        
        # 基础情况：所有层级都已选择
        if current_layer_index >= len(layer_info):
            # 获取当前组合的价格
            combination_result = self._get_current_combination_price_fast(selected_options)
            if combination_result:
                self.combinations_result.append(combination_result)
            return
        
        # 再次检查停止信号
        if self.is_stopped.is_set():
            return
        
        try:
            current_layer_data = layer_info[current_layer_index]
            current_layer_element = current_layer_data['layer']
            current_title = current_layer_data['title']
            is_last_layer = (current_layer_index == len(layer_info) - 1)
            
            # 获取当前层的所有可用选项
            available_options = self._get_layer_options_real_structure(current_layer_element, current_layer_index + 1)
            
            if not available_options:
                self.log(f"第{current_layer_index+1}层 '{current_title}' 没有可用选项，跳过")
                return
            
            self.log(f"开始遍历第{current_layer_index+1}层 '{current_title}' 的 {len(available_options)} 个选项")
            
            # 遍历当前层的每个选项
            for option_index, option in enumerate(available_options):
                # 每次迭代都检查停止信号
                if self.is_stopped.is_set():
                    self.log(f"检测到停止信号，中断第{current_layer_index+1}层遍历")
                    return
                
                # 点击选项
                self._click_sku_option(option, current_layer_index + 1)
                selected_options.append(option)
                
                # 检查停止信号
                if self.is_stopped.is_set():
                    selected_options.pop()
                    return
                
                if is_last_layer:
                    # 最后一层：获取价格
                    combination_result = self._get_current_combination_price_fast(selected_options)
                    if combination_result:
                        self.combinations_result.append(combination_result)
                        self.combination_count += 1 # Increment count here
                else:
                    # 非最后一层：继续递归
                    self._recursive_traverse_sku_layers(layer_info, current_layer_index + 1, selected_options)
                
                # 回溯：移除当前选项
                selected_options.pop()
                
                # 显式取消选择当前选项，释放上级SKU
                self._click_sku_option(option, current_layer_index + 1, is_deselect=True)
                
                # 检查停止信号
                if self.is_stopped.is_set():
                    return
                    
        except Exception as e:
            self.log(f"递归遍历第{current_layer_index+1}层时出错: {e}")

    def _click_sku_option(self, option, layer_num, is_deselect=False, max_retries=3):
        """点击SKU选项 - 带重试机制和状态检查"""
        element = option['element']
        action_type = "取消选择" if is_deselect else "选择"
        
        for retry in range(max_retries):
            try:
                # 检查元素是否仍然有效
                if not self._is_element_valid(element):
                    self.log(f"元素已失效，重新获取选项元素")
                    # 重新获取元素
                    element = self._refresh_option_element(option, layer_num)
                    if not element:
                        raise Exception("无法重新获取有效元素")
                    option['element'] = element # 更新引用
                
                # 检查当前选中状态
                try:
                    # 蓝色边框表示已选中: rgb(44, 97, 254)
                    border_color = element.value_of_css_property('border-color')
                    is_selected = 'rgb(44, 97, 254)' in border_color
                    
                    # 智能跳过逻辑
                    if not is_deselect and is_selected:
                        # self.log(f"第{layer_num}层选项 '{option['text']}' 已是选中状态，跳过点击")
                        return
                    
                    if is_deselect and not is_selected:
                        # self.log(f"第{layer_num}层选项 '{option['text']}' 已是未选中状态，跳过点击")
                        return
                except Exception as e:
                    # 获取样式失败不应阻碍点击，仅记录日志
                    # self.log(f"检查选中状态失败: {e}")
                    pass
                
                # 滚动到元素
                self.driver.execute_script("arguments[0].scrollIntoView({block: 'center'});", element)
                time.sleep(0.05)  # 极短等待确保滚动完成
                
                # 点击选项 - 使用JavaScript点击更可靠
                self.driver.execute_script("arguments[0].click();", element)
                time.sleep(0.1)  # 点击后稍作等待，让状态更新
                
                self.log(f"第{layer_num}层 {action_type}: {option['text']}")
                return  # 成功则退出重试循环
                
            except Exception as e:
                if retry < max_retries - 1:
                    self.log(f"点击第{layer_num}层选项 '{option['text']}' 失败 (重试 {retry+1}/{max_retries}): {e}")
                    time.sleep(0.2)  # 重试前短暂等待
                    continue
                else:
                    self.log(f"点击第{layer_num}层选项 '{option['text']}' 最终失败: {e}")
                    raise

    def _is_element_valid(self, element):
        """检查元素是否仍然有效"""
        try:
            # 尝试获取元素的标签名，如果失败说明元素已失效
            element.tag_name
            return element.is_displayed() and element.is_enabled()
        except:
            return False

    def _refresh_option_element(self, option, layer_num):
        """重新获取选项元素"""
        try:
            option_id = option.get('id')
            if option_id:
                # 通过ID重新获取元素
                new_element = self.driver.find_element(By.ID, option_id)
                if new_element.is_displayed():
                    option['element'] = new_element  # 更新元素引用
                    return new_element
        except:
            pass
        
        # 如果通过ID获取失败，尝试通过文本内容获取
        try:
            option_text = option['text']
            elements = self.driver.find_elements(By.XPATH, f"//span[contains(text(), '{option_text}')]")
            for elem in elements:
                parent = elem.find_element(By.XPATH, "./ancestor::div[contains(@class, 'm_ceil')]")
                if parent.is_displayed() and self._is_option_available_real_structure(parent):
                    option['element'] = parent  # 更新元素引用
                    return parent
        except:
            pass
            
        return None

    def _get_current_combination_price_fast(self, selected_options, max_retries=3):
        """快速获取当前选项组合的价格 - 带重试机制，包括实价和促销标签"""
        if not selected_options:
            return None
        
        # 组装组合文本
        combo_texts = [option['text'] for option in selected_options]
        combination_text = ' | '.join(combo_texts)
        
        for retry in range(max_retries):
            if self.is_stopped.is_set():
                break
                
            try:
                # 等待价格更新 - 使用更短的等待时间
                time.sleep(0.15)  # 等待动态加载
                
                # 获取外显价格 - 使用优化的健壮获取方法
                display_price_text = self._get_current_price_robust()
                display_currency, display_price = self._parse_price(display_price_text)
                
                # 获取实价（如果启用）
                real_currency, real_price = 'N/A', 'N/A'
                if self.extract_real_price_var.get():
                    real_currency, real_price = self._extract_real_price()
                
                # 获取SKU促销标签
                promo_tag = self._extract_sku_promotional_tag()
                
                # 检查价格是否有效
                if display_price and display_price != "价格未找到":
                    self.log(f"获取组合价格: {combination_text} = 外显:{display_currency}{display_price}, 实价:{real_currency}{real_price}, 促销:{promo_tag}")
                    return {
                        'combination': combination_text,
                        'currency': display_currency,
                        'price': display_price,
                        'real_currency': real_currency,
                        'real_price': real_price,
                        'promo_tag': promo_tag
                    }
                elif retry < max_retries - 1:
                    self.log(f"价格获取失败，重试 {retry+1}/{max_retries}")
                    time.sleep(0.2)  # 重试前稍长等待
                    
            except Exception as e:
                if retry < max_retries - 1:
                    self.log(f"获取组合价格出错，重试 {retry+1}/{max_retries}: {e}")
                    time.sleep(0.2)
                else:
                    self.log(f"获取组合价格最终失败: {e}")
                    break
        
        # 如果所有重试都失败，返回带错误信息的结果
        return {
            'combination': combination_text,
            'currency': 'N/A',
            'price': '价格获取失败',
            'real_currency': 'N/A',
            'real_price': 'N/A',
            'promo_tag': ''
        }

    def _get_current_combination_price(self, selected_options):
        """获取当前选项组合的价格（兼容旧版本）"""
        return self._get_current_combination_price_fast(selected_options)

    def _get_current_price_robust(self, max_attempts=3):
        """健壮的价格获取方法 - 基于用户指定的精确路径，带重试机制"""
        price_selectors = [
            # 用户指定的精确路径 (SKU模式)
            '#select_quantity div[class*="style_m_select_quantity_container__"] div div[class*="style_m_select_choice_option_wrapper__"] div[class*="style_m_people_price__"] div div span span',
            # 备用路径
            '#select_quantity > div.xtaro-xview > div > div.xtaro-xview > div.xtaro-xview > div > div > span > span',
            'div[class*="default_new_title_foot_warp"] div[class*="default_new_title_foot_warp_right"] div div div span',
            # 其他备用
            'span[currency="HKD"][testid*="price"]',
            'span.price_m_price_price__VrAgh',
            'div.price_m_price__FmWsJ span[currency="HKD"]',
            'span[testid="undefined-price"]',
            'span[currency="HKD"]'
        ]
        
        for attempt in range(max_attempts):
            try:
                # 优先使用精确路径
                for selector in price_selectors:
                    try:
                        elements = self.driver.find_elements(By.CSS_SELECTOR, selector)
                        for element in elements:
                            if element.is_displayed():  # 确保元素可见
                                price_text = element.text.strip()
                                if price_text and 'HK$' in price_text and len(price_text) > 3:
                                    # 验证价格格式
                                    import re
                                    if re.match(r'HK\$\d+(\.\d+)?', price_text):
                                        self.log(f"找到价格: {price_text} (选择器: {selector[:50]}...)")
                                        return price_text
                    except:
                        continue
                
                # 备用方法：查找包含HK$的所有可见元素
                elements = self.driver.find_elements(By.XPATH, "//*[contains(text(), 'HK$') and not(contains(@style, 'display: none'))]")
                for element in elements:
                    try:
                        if element.is_displayed():
                            price_text = element.text.strip()
                            if price_text and price_text.startswith('HK$'):
                                # 验证是否是价格格式
                                import re
                                if re.match(r'HK\$\d+(\.\d+)?', price_text):
                                    self.log(f"通过备用方法找到价格: {price_text}")
                                    return price_text
                    except:
                        continue
                        
                # 如果没找到，短暂等待后重试
                if attempt < max_attempts - 1:
                    time.sleep(0.1)
                    
            except Exception as e:
                if attempt < max_attempts - 1:
                    time.sleep(0.1)
                else:
                    self.log(f"价格获取方法失败: {e}")
        
        return "价格未找到"

    def _get_current_price_real_structure(self):
        """兼容旧版本的价格获取方法"""
        return self._get_current_price_robust()

    def _get_default_price(self):
        """获取默认价格（无SKU时）"""
        return self._get_current_price_real_structure()

    def _scroll_and_get_text(self, by, value, timeout=2, required=True):
        """
        增强版本，带快速跳过功能
        
        Args:
            by: Selenium By定位类型
            value: 选择器值
            timeout: 最大等待时间（默认2秒）
            required: 如果False，元素不存在时立即返回，不等待
        """
        if self.is_stopped.is_set(): 
            return "任务已停止"
        try:
            if required:
                # 对于必需元素，使用完整超时
                element = WebDriverWait(self.driver, timeout).until(
                    EC.presence_of_element_located((by, value))
                )
            else:
                # 对于可选元素，立即查找不等待
                elements = self.driver.find_elements(by, value)
                if not elements:
                    return "未找到"
                element = elements[0]
            
            # 不再滚动到元素，直接获取文本
            text = element.text.strip()
            if text:
                return text
            return "未找到"
        except TimeoutException:
            return "未找到"
        except Exception as e:
            if self.is_stopped.is_set(): 
                return "任务已停止"
            return "获取错误"
    
    def _extract_real_price(self, max_wait=1):
        """
        提取SKU选择后的实际价格（立即查找，不等待）
        """
        if not self.extract_real_price_var.get():
            return 'N/A', 'N/A'
        
        price_selector = 'div[class*="price-comp_m_footer_left_total"] div div div span span'
        
        try:
            elements = self.driver.find_elements(By.CSS_SELECTOR, price_selector)
            for element in elements:
                try:
                    if element.is_displayed():
                        price_text = element.text.strip()
                        if price_text:
                            currency, price_value = self._parse_price(price_text)
                            return currency, price_value
                except:
                    continue
        except:
            pass
        
        return 'N/A', ''
    
    def _wait_for_price_box_change(self, timeout=1.5):
        """
        监测价格框的DOM变化（优化速度）
        """
        try:
            # 价格框选择器
            price_box_selector = 'div[class*="price-comp_m_footer_price_box"]'
            
            # 等待0.1秒后开始监测
            time.sleep(0.1)
            
            # 获取初始HTML
            try:
                price_box = self.driver.find_element(By.CSS_SELECTOR, price_box_selector)
                initial_html = price_box.get_attribute('innerHTML')
            except:
                return False
            
            start_time = time.time()
            while time.time() - start_time < timeout:
                if self.is_stopped.is_set():
                    return False
                    
                try:
                    price_box = self.driver.find_element(By.CSS_SELECTOR, price_box_selector)
                    current_html = price_box.get_attribute('innerHTML')
                    
                    # 检测到变化
                    if current_html != initial_html:
                        elapsed = time.time() - start_time
                        self.log(f"价格框已更新，耗时: {elapsed:.2f}秒")
                        return True
                except:
                    pass
                
                time.sleep(0.05)  # 每50ms检查一次
            
            return False
        except Exception as e:
            self.log(f"监测价格变化时出错: {e}")
            return False
    
    def _extract_promotional_tags(self):
        """
        提取产品级别的促销标签
        使用验证过的选择器
        """
        # 用户提供的验证过的选择器
        promo_selector = 'div[class*="discount-badge_couponWarp__"] span'
        
        promo_texts = []
        try:
            elements = self.driver.find_elements(By.CSS_SELECTOR, promo_selector)
            for element in elements:
                if element.is_displayed():
                    text = element.text.strip()
                    if text and text not in promo_texts:
                        promo_texts.append(text)
        except Exception as e:
            self.log(f"提取促销标签时出错: {e}")
        
        if promo_texts:
            result = ''.join(promo_texts)  # 直接拼接，不加空格
            self.log(f"找到促销标签: {result}")
            return result
        else:
            return ''
    
    def _extract_sku_promotional_tag(self):
        """
        提取SKU级别的促销标签（SKU选择后）
        使用用户提供的精确选择器
        """
        # 用户提供的精确选择器（使用通配符）
        sku_promo_selector = 'div[class*="price-comp_m_footer_left"] div span'
        
        try:
            elements = self.driver.find_elements(By.CSS_SELECTOR, sku_promo_selector)
            for element in elements:
                if element.is_displayed():
                    text = element.text.strip()
                    # 过滤掉价格文本，只保留促销标签
                    if text and 'HK$' not in text and ('減' in text or '%' in text or '折' in text):
                        self.log(f"找到SKU促销标签: {text}")
                        return text
        except Exception as e:
            self.log(f"提取SKU促销标签时出错: {e}")
        
        return ''
    
    def _extract_product_info(self):
        """
        提取商品资讯（立即查找，不等待）
        """
        if not self.extract_product_info_var.get():
            return ''
        
        try:
            # 直接查找详情内容（懒加载已通过滚动触发）
            info_selectors = [
                '#group_1001 > div > div > div[class*="style_content_box"] > div > div',
                'div[class*="activity_product_detail_html_box"]',
                '#group_1001'
            ]
            
            for selector in info_selectors:
                try:
                    elements = self.driver.find_elements(By.CSS_SELECTOR, selector)
                    if elements:
                        info_text = elements[0].text.strip()
                        if not info_text:
                            paragraphs = elements[0].find_elements(By.TAG_NAME, 'p')
                            if paragraphs:
                                info_text = '\n'.join([p.text.strip() for p in paragraphs if p.text.strip()])
                        if info_text:
                            return info_text
                except:
                    continue
            
            self.log("未找到商品资讯内容")
            return ''
            
        except Exception as e:
            self.log(f"提取商品资讯时出错: {e}")
        
        return ''

    def _save_to_xlsx(self, data):
        """保存数据到XLSX格式"""
        if not data: 
            self.log("没有数据可保存。")
            return
        
        requested_path = getattr(self, "cli_output_path", None)
        if requested_path:
            filepath = Path(requested_path).expanduser()
        else:
            output_dir = Path(getattr(self, "cli_output_dir", None) or (Path.home() / "Downloads")).expanduser()
            timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
            
            # 根据模式选择不同的文件名
            if self.rating_only_var.get():
                filename = f"trip_score_results_{timestamp}.xlsx"
            else:
                filename = f"trip_scoreandprice_results_{timestamp}.xlsx"
            
            filepath = output_dir / filename
        
        self.log(f"正在将结果保存到: {filepath}")
        self.last_saved_filepath = None
        self.last_save_error = None
        
        try:
            filepath.parent.mkdir(parents=True, exist_ok=True)
            # 创建工作簿
            workbook = openpyxl.Workbook()
            worksheet = workbook.active
            
            # 根据模式设置工作表名称
            if self.rating_only_var.get():
                worksheet.title = "Trip评分数据"
            else:
                worksheet.title = "Trip商品数据"
            
            # 动态构建列标题
            fieldnames = ['商品ID', 'URL', '商品名称']
            if getattr(self, "include_vendor_column", False) or any('厂商' in item for item in data):
                fieldnames.insert(0, '厂商')
            
            # 仅抓取评分模式：不包含SKU列
            if not self.rating_only_var.get():
                # 添加SKU列
                for i in range(self.max_sku_layers):
                    fieldnames.append(f'SKU层级{i+1}')
                
                # 添加价格列（新字段）
                fieldnames.append('货币')  # 货币在外显价格前
                fieldnames.append('外显价格')
                if self.extract_real_price_var.get():
                    fieldnames.append('实价')
                fieldnames.append('促销标签')
            
            # 添加评分列（调整顺序：预定数 -> 评价数 -> 评分 -> ...）
            fieldnames.extend(['预定数', '评价数', '评分', '客户服务', '方便程度', '讯号状态', '中差评价数'])
            
            # 添加商品资讯列（如果启用）
            if self.extract_product_info_var.get():
                fieldnames.append('商品资讯')
            
            # 写入标题行
            for col_num, header in enumerate(fieldnames, 1):
                cell = worksheet.cell(row=1, column=col_num, value=header)
                cell.font = Font(bold=True)
            
            # 写入数据行
            for row_num, item in enumerate(data, 2):
                max_line_count = 1
                for col_num, field in enumerate(fieldnames, 1):
                    value = item.get(field, '')
                    cell = worksheet.cell(row=row_num, column=col_num, value=value)
                    
                    # 设置自动换行和顶部对齐
                    cell.alignment = Alignment(wrap_text=True, vertical='top')
                    
                    # 计算行高（针对商品资讯列）
                    if field == '商品资讯' and value:
                        # 估算行数：每行约50个字符（基于列宽50）
                        # 加上换行符的数量
                        lines = str(value).count('\n') + 1
                        char_lines = len(str(value)) / 45  # 稍微保守一点
                        current_lines = max(lines, int(char_lines) + 1)
                        max_line_count = max(max_line_count, current_lines)
                
                # 设置行高 (每行约15像素)
                if max_line_count > 1:
                    worksheet.row_dimensions[row_num].height = max_line_count * 15
            
            # 自适应列宽
            for column in worksheet.columns:
                max_length = 0
                column_letter = get_column_letter(column[0].column)
                header_text = column[0].value
                
                for cell in column:
                    try:
                        if len(str(cell.value)) > max_length:
                            max_length = len(str(cell.value))
                    except:
                        pass
                
                # 设置列宽
                if header_text == '商品资讯':
                    # 商品资讯列固定较宽，依靠换行
                    adjusted_width = 50
                elif header_text == '商品名称':
                    adjusted_width = 40
                elif header_text == '厂商':
                    adjusted_width = 20
                elif header_text == 'URL':
                    adjusted_width = 30
                else:
                    adjusted_width = min(max(max_length + 2, 8), 30)
                    
                worksheet.column_dimensions[column_letter].width = adjusted_width
            
            # 保存文件
            workbook.save(filepath)
            self.last_saved_filepath = str(filepath)
            self.log("XLSX文件保存成功！")
            messagebox.showinfo("完成", f"数据已成功保存到：\n{filepath}\n\n共保存 {len(data)} 条记录")
            
        except Exception as e:
            self.last_save_error = str(e)
            self.log(f"错误：保存XLSX文件失败: {e}")
            messagebox.showerror("保存失败", f"无法保存XLSX文件。\n\n错误详情: {e}")

    def _save_to_csv(self, data):
        """兼容性方法 - 现在调用XLSX保存"""
        self._save_to_xlsx(data)

    def clear_data(self):
        """清空已获取的数据"""
        if not self.results_data:
            return
            
        if messagebox.askyesno("确认", "确定要清空所有已抓取的数据吗？"):
            # 清空数据列表
            self.results_data = []
            
            # 清空TreeView
            for item in self.results_tree.get_children():
                self.results_tree.delete(item)
            
            self.log("已清空所有数据。")
            
            # 更新按钮状态
            self.update_button_states(False)

    def on_closing(self):
        """程序关闭处理"""
        if messagebox.askokcancel("退出", "您确定要退出程序吗？\n如果浏览器已打开，将会被关闭。"):
            try:
                self.log("程序正在退出...")
                
                # 恢复屏保设置
                try:
                    self.screen_saver_preventer.stop_prevention()
                except:
                    pass
                
                # 停止所有执行中的任务
                self.stop_scraping()
                
                def close_and_exit():
                    try:
                        self._safe_driver_quit()
                    except:
                        pass
                    self.root.after(0, self.root.destroy)

                close_thread = threading.Thread(target=close_and_exit, daemon=True)
                close_thread.start()

                # 设置超时，防止无限等待
                self.root.after(20000, self.root.destroy)
                    
            except Exception as e:
                self.log(f"退出时发生错误: {e}")
                self.root.destroy()

if __name__ == "__main__":
    import multiprocessing
    multiprocessing.freeze_support()
    
    root = tk.Tk()
    app = WebScraperApp(root)
    root.protocol("WM_DELETE_WINDOW", app.on_closing)
    root.mainloop()
