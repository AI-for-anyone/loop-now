---
layout: home

hero:
  name: Loop Now
  text: 本地多 Agent 协作工作台
  tagline: 让多个 AI Agent 在本地协作，用插件扩展它们的能力。接入 CLI 与 MCP，订阅事件、编写 Hook，把项目、对话和任务放在一个工作台中。
  image:
    src: /favicon.svg
    alt: Loop Now 图标
    width: 140
  actions:
    - theme: brand
      text: 快速上手
      link: /zh/guide/getting-started
    - theme: alt
      text: GitHub
      link: https://github.com/AI-for-anyone/loop-now
    - theme: alt
      text: English
      link: /
    - theme: alt
      text: v0.1.0
      link: https://github.com/AI-for-anyone/loop-now/releases

features:
  - icon: 🤝
    title: 组建 Agent 团队
    details: 按项目组织 Agent，分别配置模型、思考等级、指令、Skills、仓库与工具权限，并指定 Manager 协调工作。
  - icon: 🧠
    title: 保留工作上下文
    details: 持续使用 Agent 的常态会话与工作目录。任务使用独立副本，同一 Agent 在同一任务中连续复用自己的目录和会话。
  - icon: 📬
    title: 通过 Inbox 协作
    details: Agent 与人之间可以发消息、提问、回复；忙碌时选择排队或打断，sleep 等待中的 Agent 可被新消息唤醒。
  - icon: 🔀
    title: 运行可观察的任务流程
    details: 查看节点、条件分支、有次数限制的审查回路、重试与成果。任务开始后固定流程和已捕获的配置。
  - icon: 🧩
    title: 用插件扩展能力
    details: 在同一插件中组合 CLI、MCP、事件 Hook、账号、权限和 Skills，接入团队需要的工具与服务。
  - icon: 🔍
    title: 看清执行过程
    details: 流式阅读回复、发送图片、渲染 Markdown 和 Mermaid，在每条回复的运行时间线中查看模型用量、工具命令、耗时和失败原因。
---

<div class="ln-hero-image">

![Loop Now 控制台：Agent 对话、图表渲染与紧凑输入框](/assets/console.png)

</div>

<p class="ln-caption">真实控制台，使用演示数据。界面支持中文和英文，按浏览器语言或在设置里切换。</p>

## 从开发到审查，都在一个对话里

创建 Manager、开发和审查三个 Agent，配置它们使用的仓库、模型和工具，然后对 Manager 说：

> 实现这个功能，让审查 Agent 检查后给我结果。需要我决定的事情通过 Inbox 提问。

任务流程可以从开发进入审查，也可以在限定次数内返回修改。各成员在自己的任务上下文中工作，你能沿途查看消息和成果。具体开发、QA 或部署流程由项目指令与 Skills 定义，平台不预设某一种业务流程。

## 插件是核心扩展入口

一个插件就是一个目录，`package.json` 是它唯一的清单。**Agent 半边**是标准的 [Pi 包](https://pi.dev/packages)，随授权加载进每个 Agent 的 Pi 进程；**平台半边**运行在服务端旁边一个常驻的 Node 插件宿主里，用按权限签发的插件令牌调用平台接口。

清单说了算：订阅未声明的事件、调用未声明的接口都会失败；安装前会向用户展示插件的两半内容、事件和权限。[阅读插件体系概览 →](/zh/plugins/)

## 早期个人版

本项目面向**本地单实例**：控制台没有登录功能，应通过本机回环地址访问；凭证以明文保存在你自己的 PostgreSQL 中，请保护好数据库访问与备份。使用模型或工具供应商时，相关数据仍会发送给对应服务。

平台运行在你的电脑上，以 [Pi](https://github.com/earendil-works/pi) 为执行引擎。使用哪些模型、账号、代码仓库和工具，由你为各个 Agent 配置。
