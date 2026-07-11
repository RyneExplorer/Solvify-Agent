# DOCX 预览容器挂载修复设计

## 问题

DOCX 文件加载完成后，页面仍处于 loading 分支，DOCX 预览容器尚未挂载。此时即使等待 `nextTick`，也无法取得容器 ref，最终报“DOCX 预览容器初始化失败”。

## 方案

- 原文件 Blob 获取成功后先结束 loading
- 等待 `nextTick` 让 DOCX 条件分支和容器 ref 完成挂载
- 容器存在后调用 `docx-preview` 渲染
- 渲染失败时继续使用现有错误区域展示错误
- 其他文件类型预览流程保持不变

## 验证

- DOCX 文件可正常挂载容器并渲染
- DOCX 渲染失败时显示实际渲染错误
- Markdown、TXT、HTML、JSON、CSV、PDF 和图片预览不受影响
- 前端生产构建通过
