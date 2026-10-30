export const agents = {
  create: '新建 Agent',
  search: { label: '搜索 Agent', placeholder: '搜索名称或地址', noMatch: '没有匹配的 Agent' },
  empty: '还没有 Agent。',
  notFound: '该 Agent 不存在或已删除。',
  busy: '正忙',
  idle: '空闲',
  sessionMissing: '会话丢失',
  tabs: { chat: '对话', config: '配置', folder: '文件夹', inbox: '收件箱' },
  address: {
    label: '地址',
    missing: '这个 Agent 暂时没有地址：名称不满足命名规则，改名后才有地址。给它发 Inbox 消息时改用内部 ID。',
  },
  selectHint: '从左侧栏选择一个 Agent。',
  manager: 'Manager',
  managerTitle: '项目 Manager',
} as const

export const createAgent = {
  title: '新建 Agent',
  submit: '创建',
  modelRequired: '请选择模型',
  nameRequired: '请填写显示名称',
  nameConflict: '项目内已有同名的 Agent，请换一个名称',
  platformNameConflict: '平台内已有同名的 Agent，请换一个名称',
  nameRules: {
    separator: '名称不能含“/”或“#”',
    atStart: '名称不能以“@”开头',
    whitespace: '名称首尾不能有空白',
    control: '名称不能含控制字符',
    uuid: '名称不能是 UUID 格式',
    tooLong: '名称最长 100 个字符',
  },
} as const
