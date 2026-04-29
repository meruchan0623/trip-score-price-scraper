# Trip.com 评分+SKU价格抓取工具

从 Trip.com 商品页面提取评分、评价数、预订数及所有SKU组合价格。支持 GUI/CLI 双模式，基于 undetected-chromedriver + Selenium。

## 项目结构

`
trip-score-price-scraper/
├── main.py          # 程序入口
├── gui.py           # GUI 界面 (Tkinter)
├── cli.py           # 命令行接口
├── service.py       # 核心业务逻辑
├── models.py        # 数据模型
├── requirements.txt # Python 依赖
└── *.pyw            # Windows 可执行入口
`

## 安装与运行

\\ash
# 安装依赖
pip install -r requirements.txt

# 运行
python trip-score-price-scraper.pyw
\
## 构建 (PyInstaller)

\\ash
pyinstaller trip-score-price-scraper.spec
\
## 依赖

详见 
equirements.txt。
