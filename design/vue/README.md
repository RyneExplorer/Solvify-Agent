# 知识助理 - Knowledge Assistant

基于 Vue 3 + TypeScript + Tailwind CSS 的知识库问答系统主页面。

## 项目结构

```
knowledge-assistant/
├── index.html              # 入口 HTML
├── package.json            # 依赖配置
├── vite.config.ts          # Vite 配置
├── tsconfig.json           # TypeScript 配置
├── tailwind.config.js      # Tailwind 配置
├── postcss.config.js       # PostCSS 配置
└── src/
    ├── main.ts              # 入口文件
    ├── App.vue              # 根组件
    ├── style.css            # 全局样式
    ├── vite-env.d.ts       # Vite 类型声明
    └── components/
        ├── Sidebar.vue      # 左侧边栏组件
        ├── ChatHome.vue     # 主内容区组件
        └── ChatInput.vue    # 输入框组件
```

## 安装与运行

```bash
# 安装依赖
npm install

# 开发模式
npm run dev

# 构建生产版本
npm run build
```

## 功能特性

- ✅ 左侧边栏：Logo、主导航、历史对话列表、用户区
- ✅ 主内容区：标题、输入框、工具栏、发送按钮
- ✅ 响应式设计
- ✅ 绿色主题配色
- ✅ 组件化架构

## 技术栈

- Vue 3 (Composition API + `<script setup>`)
- TypeScript
- Tailwind CSS
- Vite
