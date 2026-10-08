# 离线页结构报告示例

`lesson.html`、`lob.html`和`secondary.html`由正式visualize命令和固定夹具生成，不需要服务或网络。可本地双击打开；分别用于记录/布局、LOB路径、182页二级树学习。未嵌入字节的页会提示重新生成，并非解析失败。

复跑6份报告：`python3 scripts/verify_visual_reports.py --out /tmp/new-visual-reports`。本目录仅保存其中三份便于查看，完整统计见verification.json；默认只做数据/原字节/脚本语法验证，工具浏览器因file://策略受限；用户已打开示例并确认页号定位、父子页点击、记录高亮及LOB导航正常。

使用说明见[第81章](../../docs/format/81-visual-report-model.md)和[第82章](../../docs/format/82-visual-report-validation.md)。
