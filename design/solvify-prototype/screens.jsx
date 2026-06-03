// Solvify-Agent Screens
// White-and-blue design v2

// ========== LOGIN SCREEN ==========
function LoginScreen({ onLogin }) {
  const [email, setEmail] = React.useState('');
  const [password, setPassword] = React.useState('');
  const [isRegister, setIsRegister] = React.useState(false);
  const [name, setName] = React.useState('');

  const handleSubmit = (e) => {
    e.preventDefault();
    onLogin({ name: name || email.split('@')[0] || 'Admin', email: email || 'admin@solvify.ai' });
  };

  return (
    <div data-screen-label="01 Login" style={{
      minHeight: '100vh', display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center',
      backgroundColor: '#ffffff', fontFamily: "'Inter', sans-serif",
    }}>
      <div style={{ marginBottom: '48px', textAlign: 'center' }}>
        <div style={{
          fontSize: '36px', fontWeight: 700, color: '#0f172a',
          fontFamily: "'Space Grotesk', sans-serif", letterSpacing: '-0.03em',
        }}>
          <span style={{ color: '#2563eb' }}>Solvify</span>-Agent
        </div>
        <div style={{ fontSize: '14px', color: '#94a3b8', marginTop: '8px', fontWeight: 400 }}>
          企业级智能知识管理平台
        </div>
      </div>
      <div style={{
        backgroundColor: '#ffffff', border: '1px solid #e2e8f0',
        borderRadius: '16px', padding: '32px', width: '380px',
        boxSizing: 'border-box', boxShadow: '0 1px 3px rgba(0,0,0,0.04)',
      }}>
        <h2 style={{
          fontSize: '20px', fontWeight: 600, color: '#0f172a',
          margin: '0 0 24px', textAlign: 'center',
          fontFamily: "'Space Grotesk', sans-serif",
        }}>
          {isRegister ? '创建账号' : '欢迎回来'}
        </h2>
        <form onSubmit={handleSubmit}>
          {isRegister && (
            <div style={{ marginBottom: '16px' }}>
              <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, color: '#475569', marginBottom: '6px' }}>姓名</label>
              <Input placeholder="请输入姓名" value={name} onChange={e => setName(e.target.value)} />
            </div>
          )}
          <div style={{ marginBottom: '16px' }}>
            <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, color: '#475569', marginBottom: '6px' }}>邮箱</label>
            <Input type="email" placeholder="name@company.com" value={email} onChange={e => setEmail(e.target.value)} />
          </div>
          <div style={{ marginBottom: '24px' }}>
            <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, color: '#475569', marginBottom: '6px' }}>密码</label>
            <Input type="password" placeholder="请输入密码" value={password} onChange={e => setPassword(e.target.value)} />
          </div>
          <Button style={{ width: '100%', justifyContent: 'center', padding: '12px 20px' }}>
            {isRegister ? '注册' : '登录'}
          </Button>
        </form>
        <div style={{ marginTop: '20px', textAlign: 'center', fontSize: '14px', color: '#94a3b8' }}>
          {isRegister ? '已有账号？' : '没有账号？'}
          <a onClick={() => setIsRegister(!isRegister)} style={{ color: '#2563eb', cursor: 'pointer', marginLeft: '4px' }}>
            {isRegister ? '立即登录' : '注册'}
          </a>
        </div>
      </div>
      <div style={{ marginTop: '24px', fontSize: '12px', color: '#94a3b8' }}>
        Powered by RAG + Agent
      </div>
    </div>
  );
}

// ========== DASHBOARD SCREEN ==========
function DashboardScreen({ user }) {
  return (
    <div data-screen-label="02 Dashboard" style={{ padding: '32px 40px' }}>
      <div style={{ marginBottom: '32px' }}>
        <h1 style={{
          fontSize: '28px', fontWeight: 700, color: '#0f172a', margin: 0,
          fontFamily: "'Space Grotesk', sans-serif", letterSpacing: '-0.02em',
        }}>
          欢迎回来，{user.name}
        </h1>
        <p style={{ fontSize: '14px', color: '#94a3b8', margin: '8px 0 0' }}>
          这是你的知识管理概览
        </p>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(4, 1fr)', gap: '12px', marginBottom: '32px' }}>
        <StatCard label="知识库" value="12" icon="&#128218;" />
        <StatCard label="文档总数" value="1,847" icon="&#128196;" />
        <StatCard label="本月问答" value="3,291" icon="&#128172;" />
        <StatCard label="存储使用" value="4.2 GB" icon="&#128190;" />
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '2fr 1fr', gap: '16px' }}>
        <Card>
          <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 16px', fontFamily: "'Space Grotesk', sans-serif" }}>最近活动</h3>
          {[
            { detail: '产品需求文档 v3.2.pdf', action: '上传文档', kb: '产品文档库', time: '10 分钟前' },
            { detail: '如何配置向量数据库？', action: '问答会话', kb: '技术支持库', time: '1 小时前' },
            { detail: '新增 23 篇技术文档', action: '知识库更新', kb: '技术文档库', time: '3 小时前' },
            { detail: 'API 接口文档 Wiki 已生成', action: 'Wiki 生成', kb: '开发文档库', time: '昨天' },
          ].map((item, i) => (
            <div key={i} style={{
              display: 'flex', alignItems: 'center', justifyContent: 'space-between',
              padding: '12px 0', borderBottom: i < 3 ? '1px solid #f1f5f9' : 'none',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                <div style={{ width: '6px', height: '6px', borderRadius: '50%', backgroundColor: '#2563eb' }} />
                <div>
                  <div style={{ fontSize: '14px', color: '#0f172a' }}>{item.detail}</div>
                  <div style={{ fontSize: '12px', color: '#94a3b8', marginTop: '2px' }}>{item.action} &middot; {item.kb}</div>
                </div>
              </div>
              <span style={{ fontSize: '12px', color: '#94a3b8' }}>{item.time}</span>
            </div>
          ))}
        </Card>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '12px' }}>
          <Card>
            <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 16px', fontFamily: "'Space Grotesk', sans-serif" }}>快速操作</h3>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
              <Button variant="secondary" style={{ justifyContent: 'center', width: '100%' }}>+ 新建知识库</Button>
              <Button variant="secondary" style={{ justifyContent: 'center', width: '100%' }}>上传文档</Button>
              <Button variant="secondary" style={{ justifyContent: 'center', width: '100%' }}>开始问答</Button>
            </div>
          </Card>
          <Card>
            <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 16px', fontFamily: "'Space Grotesk', sans-serif" }}>系统状态</h3>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '12px', fontSize: '14px' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span style={{ color: '#94a3b8' }}>RAG 引擎</span>
                <Badge variant="success">运行中</Badge>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span style={{ color: '#94a3b8' }}>向量数据库</span>
                <Badge variant="success">已连接</Badge>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span style={{ color: '#94a3b8' }}>AI 模型</span>
                <Badge variant="blue">GPT-4</Badge>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between' }}>
                <span style={{ color: '#94a3b8' }}>本月配额</span>
                <Badge variant="warning">68/100</Badge>
              </div>
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}

// ========== QA CHAT SCREEN ==========
function QAScreen() {
  const [messages, setMessages] = React.useState([
    {
      role: 'assistant',
      content: '你好！我是 Solvify 智能助手，基于你的知识库和联网搜索提供专业回答。请选择知识库和搜索模式后开始提问。',
      sources: [],
    },
  ]);
  const [input, setInput] = React.useState('');
  const [searchMode, setSearchMode] = React.useState('quick');
  const [selectedKB, setSelectedKB] = React.useState('技术文档库');
  const [selectedModel, setSelectedModel] = React.useState('default');
  const [searchToolConfigured] = React.useState(true);
  const [savedResults, setSavedResults] = React.useState(new Set());
  const chatEndRef = React.useRef(null);

  const handleSend = () => {
    if (!input.trim()) return;
    const userMsg = { role: 'user', content: input };
    const isQuick = searchMode === 'quick';
    const aiMsg = {
      role: 'assistant',
      content: isQuick
        ? `基于「${selectedKB}」的快速检索结果，以下是关于「${input}」的回答：\n\n这是一个示例回答。快速检索模式直接使用 BM25 + Dense 向量检索知识库，返回最相关的结果，响应速度快（≤ 3 秒）。`
        : `基于「${selectedKB}」的深度检索结果，以下是关于「${input}」的回答：\n\n深度模式先理解问题上下文，进行多轮检索。若知识库无结果或置信度低，自动触发联网搜索获取最新信息。\n\n回答支持多步推理，对于复杂问题会自动编排 ReAct Agent 流程。`,
      sources: isQuick
        ? [
            { type: 'kb', title: '产品架构设计文档', section: '3.2 向量检索方案' },
            { type: 'kb', title: '技术实现指南', section: 'RAG 配置说明' },
          ]
        : [
            { type: 'kb', title: '产品架构设计文档', section: '3.2 向量检索方案' },
            { type: 'kb', title: '技术实现指南', section: 'RAG 配置说明' },
            { type: 'web', title: '最新技术博客 - RAG 最佳实践', url: 'example.com' },
          ],
      searchMode: searchMode,
    };
    setMessages(prev => [...prev, userMsg, aiMsg]);
    setInput('');
  };

  const handleSaveResult = (msgIndex) => {
    setSavedResults(prev => new Set([...prev, msgIndex]));
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSend(); }
  };

  React.useEffect(() => {
    if (chatEndRef.current) chatEndRef.current.scrollTop = chatEndRef.current.scrollHeight;
  }, [messages]);

  return (
    <div data-screen-label="03 QA Chat" style={{ display: 'flex', flexDirection: 'column', height: 'calc(100vh - 56px)' }}>
      <div style={{
        padding: '12px 24px', borderBottom: '1px solid #e2e8f0', backgroundColor: '#ffffff',
        display: 'flex', alignItems: 'center', gap: '12px', flexShrink: 0, flexWrap: 'wrap',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <span style={{ fontSize: '12px', fontWeight: 600, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.05em' }}>知识库</span>
          <Select value={selectedKB} onChange={e => setSelectedKB(e.target.value)}>
            <option>全部知识库</option><option>技术文档库</option><option>产品文档库</option><option>客服知识库</option>
          </Select>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <span style={{ fontSize: '12px', fontWeight: 600, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.05em' }}>检索模式</span>
          <div style={{ display: 'flex', border: '1px solid #e2e8f0', borderRadius: '10px', overflow: 'hidden' }}>
            {[{ key: 'quick', label: '快速检索' }, { key: 'deep', label: '深度模式' }].map(m => {
              const disabled = m.key === 'deep' && !searchToolConfigured;
              return (
                <button key={m.key} onClick={() => !disabled && setSearchMode(m.key)} style={{
                  padding: '6px 14px', fontSize: '12px', cursor: disabled ? 'not-allowed' : 'pointer', border: 'none',
                  backgroundColor: searchMode === m.key ? '#2563eb' : '#ffffff',
                  color: searchMode === m.key ? '#ffffff' : disabled ? '#d1d5db' : '#94a3b8',
                  fontWeight: searchMode === m.key ? 500 : 400, transition: 'all 0.15s ease',
                  fontFamily: "'Inter', sans-serif",
                  position: 'relative',
                }}>
                  {m.label}
                  {disabled && (
                    <span style={{ fontSize: '10px', marginLeft: '4px', color: '#d1d5db' }} title="请先配置搜索工具 API Key">🔒</span>
                  )}
                </button>
              );
            })}
          </div>
          {!searchToolConfigured && searchMode === 'quick' && (
            <span style={{ fontSize: '11px', color: '#94a3b8' }}>深度模式需配置搜索工具</span>
          )}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
          <span style={{ fontSize: '12px', fontWeight: 600, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.05em' }}>模型</span>
          <Select value={selectedModel} onChange={e => setSelectedModel(e.target.value)} style={{ minWidth: '140px' }}>
            <option value="default">系统默认</option>
            <option value="gpt4">GPT-4</option>
            <option value="claude3">Claude 3</option>
            <option value="qwen">通义千问</option>
          </Select>
        </div>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: '8px' }}>
          <Button variant="ghost" style={{ fontSize: '12px' }}>推荐问题</Button>
          <Button variant="ghost" style={{ fontSize: '12px' }}>清空会话</Button>
        </div>
      </div>

      <div ref={chatEndRef} style={{ flex: 1, overflowY: 'auto', padding: '24px', backgroundColor: '#f8fafc' }}>
        <div style={{ maxWidth: '800px', margin: '0 auto' }}>
          {messages.map((msg, i) => (
            <div key={i} style={{
              display: 'flex', justifyContent: msg.role === 'user' ? 'flex-end' : 'flex-start', marginBottom: '20px',
            }}>
              {msg.role === 'assistant' && (
                <div style={{
                  width: '28px', height: '28px', borderRadius: '8px',
                  backgroundColor: '#2563eb', color: '#ffffff',
                  display: 'flex', alignItems: 'center', justifyContent: 'center',
                  fontSize: '12px', fontWeight: 700, flexShrink: 0, marginRight: '12px',
                  fontFamily: "'Space Grotesk', sans-serif",
                }}>S</div>
              )}
              <div style={{
                maxWidth: '75%', padding: '14px 18px', borderRadius: '16px',
                backgroundColor: msg.role === 'user' ? '#2563eb' : '#ffffff',
                color: msg.role === 'user' ? '#ffffff' : '#0f172a',
                border: msg.role === 'user' ? 'none' : '1px solid #e2e8f0',
                fontSize: '14px', lineHeight: 1.7, whiteSpace: 'pre-wrap',
                boxShadow: msg.role === 'user' ? 'none' : '0 1px 2px rgba(0,0,0,0.04)',
              }}>
                {msg.content}
                {msg.sources && msg.sources.length > 0 && (
                  <div style={{
                    marginTop: '14px', paddingTop: '14px',
                    borderTop: msg.role === 'user' ? '1px solid rgba(255,255,255,0.2)' : '1px solid #e2e8f0',
                    display: 'flex', flexWrap: 'wrap', gap: '6px', alignItems: 'center',
                  }}>
                    <span style={{ fontSize: '12px', color: msg.role === 'user' ? 'rgba(255,255,255,0.7)' : '#94a3b8', marginRight: '4px' }}>来源:</span>
                    {msg.sources.map((s, j) => (
                      <Badge key={j} variant={s.type === 'kb' ? 'blue' : 'success'}>{s.title}</Badge>
                    ))}
                    {msg.searchMode === 'deep' && msg.sources.some(s => s.type === 'web') && (
                      <button
                        onClick={() => handleSaveResult(i)}
                        disabled={savedResults.has(i)}
                        style={{
                          marginLeft: '8px', padding: '4px 12px', fontSize: '12px', borderRadius: '8px',
                          border: 'none', cursor: savedResults.has(i) ? 'default' : 'pointer',
                          backgroundColor: savedResults.has(i) ? '#f0fdf4' : '#2563eb',
                          color: savedResults.has(i) ? '#16a34a' : '#ffffff',
                          fontWeight: 500, transition: 'all 0.15s ease',
                          fontFamily: "'Inter', sans-serif",
                        }}
                      >
                        {savedResults.has(i) ? '✓ 已保存' : '保存到联网搜索知识库'}
                      </button>
                    )}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      </div>

      <div style={{ padding: '16px 24px', borderTop: '1px solid #e2e8f0', backgroundColor: '#ffffff', flexShrink: 0 }}>
        <div style={{ maxWidth: '800px', margin: '0 auto', position: 'relative' }}>
          <textarea
            value={input} onChange={e => setInput(e.target.value)} onKeyDown={handleKeyDown}
            placeholder="输入你的问题..."
            style={{
              width: '100%', backgroundColor: '#f8fafc', border: '1px solid #e2e8f0',
              borderRadius: '16px', padding: '14px 50px 14px 18px', fontSize: '14px',
              lineHeight: '20px', color: '#0f172a', outline: 'none', resize: 'none',
              minHeight: '48px', maxHeight: '120px', fontFamily: "'Inter', sans-serif",
            }}
            rows={1}
          />
          <button onClick={handleSend} style={{
            position: 'absolute', right: '8px', bottom: '8px',
            width: '32px', height: '32px', borderRadius: '10px',
            backgroundColor: '#2563eb', color: '#ffffff', border: 'none',
            cursor: 'pointer', display: 'flex', alignItems: 'center', justifyContent: 'center',
          }}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
              <path d="M22 2 11 13"/><path d="M22 2 15 22 11 13 2 9z"/>
            </svg>
          </button>
        </div>
      </div>
    </div>
  );
}

// ========== KNOWLEDGE BASE SCREEN ==========
function KnowledgeBaseScreen() {
  const knowledgeBases = [
    { id: 1, name: '技术文档库', category: '技术', docs: 342, size: '2.1 GB', status: 'ready', updated: '2 小时前', source: 'self' },
    { id: 2, name: '产品需求库', category: '产品', docs: 156, size: '890 MB', status: 'ready', updated: '1 天前', source: 'self' },
    { id: 3, name: '客服知识库', category: '客服', docs: 892, size: '1.5 GB', status: 'processing', updated: '30 分钟前', source: 'self' },
    { id: 4, name: '钉钉-技术分享', category: '技术', docs: 234, size: '560 MB', status: 'ready', updated: '1 小时前', source: 'dingtalk' },
    { id: 5, name: '飞书-产品文档', category: '产品', docs: 189, size: '720 MB', status: 'ready', updated: '2 天前', source: 'feishu' },
    { id: 6, name: 'Notion-开发笔记', category: '技术', docs: 67, size: '180 MB', status: 'ready', updated: '3 天前', source: 'notion' },
    { id: 7, name: '联网搜索知识库', category: '综合', docs: 45, size: '12 MB', status: 'ready', updated: '1 小时前', source: 'web_search' },
  ];

  const sourceLabels = {
    self: null,
    dingtalk: { text: '钉钉同步', color: '#2563eb' },
    feishu: { text: '飞书同步', color: '#7c3aed' },
    notion: { text: 'Notion 同步', color: '#000000' },
    web_search: { text: '联网搜索', color: '#16a34a' },
  };

  return (
    <div data-screen-label="04 Knowledge Base" style={{ padding: '32px 40px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h1 style={{ fontSize: '28px', fontWeight: 700, color: '#0f172a', margin: 0, fontFamily: "'Space Grotesk', sans-serif", letterSpacing: '-0.02em' }}>知识库管理</h1>
          <p style={{ fontSize: '14px', color: '#94a3b8', margin: '8px 0 0' }}>管理自建知识库，查看第三方平台同步的知识库</p>
        </div>
        <div style={{ display: 'flex', gap: '8px' }}>
          <Button variant="secondary">同步知识库</Button>
          <Button>+ 新建知识库</Button>
        </div>
      </div>

      {/* Filter tabs */}
      <div style={{ display: 'flex', gap: '8px', marginBottom: '20px' }}>
        <SearchInput placeholder="搜索知识库..." value="" onChange={() => {}} style={{ width: '320px' }} />
        <Select style={{ width: '160px' }}><option>全部分类</option><option>技术</option><option>产品</option><option>客服</option><option>培训</option></Select>
        <Select style={{ width: '160px' }}><option>全部来源</option><option>自建</option><option>钉钉同步</option><option>飞书同步</option><option>Notion 同步</option><option>联网搜索</option></Select>
      </div>

      {/* Self-created section label */}
      <div style={{ fontSize: '12px', fontWeight: 600, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '12px' }}>
        自建知识库
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: '12px', marginBottom: '32px' }}>
        {knowledgeBases.filter(kb => kb.source === 'self').map(kb => (
          <Card key={kb.id} style={{ cursor: 'pointer' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: '14px' }}>
              <div>
                <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: 0 }}>{kb.name}</h3>
                <span style={{ fontSize: '12px', color: '#94a3b8' }}>{kb.category}</span>
              </div>
              <Badge variant={kb.status === 'ready' ? 'success' : 'warning'}>{kb.status === 'ready' ? '已就绪' : '处理中'}</Badge>
            </div>
            <div style={{ display: 'flex', gap: '24px', fontSize: '13px', color: '#94a3b8', marginBottom: '14px' }}>
              <span>{kb.docs} 篇文档</span><span>{kb.size}</span>
            </div>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span style={{ fontSize: '12px', color: '#94a3b8' }}>更新于 {kb.updated}</span>
              <div style={{ display: 'flex', gap: '4px' }}>
                <Button variant="ghost" style={{ fontSize: '12px', padding: '4px 12px' }}>编辑</Button>
                <Button variant="ghost" style={{ fontSize: '12px', padding: '4px 12px' }}>文档</Button>
              </div>
            </div>
          </Card>
        ))}
      </div>

      {/* Synced section label */}
      <div style={{ fontSize: '12px', fontWeight: 600, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '12px' }}>
        同步知识库
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: '12px', marginBottom: '32px' }}>
        {knowledgeBases.filter(kb => ['dingtalk', 'feishu', 'notion'].includes(kb.source)).map(kb => {
          const src = sourceLabels[kb.source];
          return (
            <Card key={kb.id} style={{ cursor: 'pointer' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: '14px' }}>
                <div>
                  <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: 0 }}>{kb.name}</h3>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginTop: '4px' }}>
                    <span style={{ fontSize: '12px', color: '#94a3b8' }}>{kb.category}</span>
                    {src && (
                      <span style={{
                        fontSize: '11px', fontWeight: 500, color: src.color,
                        backgroundColor: src.color + '15', padding: '1px 8px',
                        borderRadius: '9999px', display: 'inline-block',
                      }}>{src.text}</span>
                    )}
                  </div>
                </div>
                <Badge variant={kb.status === 'ready' ? 'success' : 'warning'}>{kb.status === 'ready' ? '已就绪' : '同步中'}</Badge>
              </div>
              <div style={{ display: 'flex', gap: '24px', fontSize: '13px', color: '#94a3b8', marginBottom: '14px' }}>
                <span>{kb.docs} 篇文档</span><span>{kb.size}</span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <span style={{ fontSize: '12px', color: '#94a3b8' }}>最后同步 {kb.updated}</span>
                <div style={{ display: 'flex', gap: '4px' }}>
                  <Button variant="ghost" style={{ fontSize: '12px', padding: '4px 12px' }}>查看</Button>
                  <Button variant="ghost" style={{ fontSize: '12px', padding: '4px 12px' }}>立即同步</Button>
                </div>
              </div>
            </Card>
          );
        })}
      </div>

      {/* Web search section label */}
      <div style={{ fontSize: '12px', fontWeight: 600, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.05em', marginBottom: '12px' }}>
        联网搜索知识库
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(340px, 1fr))', gap: '12px' }}>
        {knowledgeBases.filter(kb => kb.source === 'web_search').map(kb => {
          const src = sourceLabels[kb.source];
          return (
            <Card key={kb.id} style={{ cursor: 'pointer' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: '14px' }}>
                <div>
                  <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: 0 }}>{kb.name}</h3>
                  <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginTop: '4px' }}>
                    <span style={{ fontSize: '12px', color: '#94a3b8' }}>{kb.category}</span>
                    {src && (
                      <span style={{
                        fontSize: '11px', fontWeight: 500, color: src.color,
                        backgroundColor: src.color + '15', padding: '1px 8px',
                        borderRadius: '9999px', display: 'inline-block',
                      }}>{src.text}</span>
                    )}
                  </div>
                </div>
                <Badge variant="success">{kb.status === 'ready' ? '已就绪' : '处理中'}</Badge>
              </div>
              <div style={{ display: 'flex', gap: '24px', fontSize: '13px', color: '#94a3b8', marginBottom: '14px' }}>
                <span>{kb.docs} 条记录</span><span>{kb.size}</span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                <span style={{ fontSize: '12px', color: '#94a3b8' }}>更新于 {kb.updated}</span>
                <div style={{ display: 'flex', gap: '4px' }}>
                  <Button variant="ghost" style={{ fontSize: '12px', padding: '4px 12px' }}>查看</Button>
                </div>
              </div>
              <div style={{ marginTop: '10px', padding: '10px 12px', backgroundColor: '#f0fdf4', borderRadius: '8px', fontSize: '12px', color: '#16a34a' }}>
                由深度模式联网搜索结果自动保存，存储计入个人配额
              </div>
            </Card>
          );
        })}
      </div>
    </div>
  );
}

// ========== DOCUMENTS SCREEN ==========
function DocumentsScreen() {
  const documents = [
    { name: '产品需求文档 v3.2.pdf', size: '2.4 MB', status: 'ready', type: 'PDF', uploaded: '2024-01-15' },
    { name: 'API 接口设计.docx', size: '890 KB', status: 'ready', type: 'Word', uploaded: '2024-01-14' },
    { name: '系统架构图.png', size: '1.2 MB', status: 'processing', type: '图片', uploaded: '2024-01-14' },
    { name: '数据库设计文档.md', size: '45 KB', status: 'ready', type: 'Markdown', uploaded: '2024-01-13' },
    { name: '部署指南.html', size: '120 KB', status: 'ready', type: 'HTML', uploaded: '2024-01-12' },
    { name: '测试用例集.xlsx', size: '3.1 MB', status: 'ready', type: 'Excel', uploaded: '2024-01-11' },
    { name: '用户手册.pdf', size: '5.6 MB', status: 'error', type: 'PDF', uploaded: '2024-01-10' },
  ];

  return (
    <div data-screen-label="05 Documents" style={{ padding: '32px 40px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h1 style={{ fontSize: '28px', fontWeight: 700, color: '#0f172a', margin: 0, fontFamily: "'Space Grotesk', sans-serif", letterSpacing: '-0.02em' }}>文档管理</h1>
          <p style={{ fontSize: '14px', color: '#94a3b8', margin: '8px 0 0' }}>上传、管理和编辑知识库中的文档</p>
        </div>
        <div style={{ display: 'flex', gap: '8px' }}>
          <Button variant="secondary">多源导入</Button>
          <Button>+ 上传文档</Button>
        </div>
      </div>
      <div style={{
        border: '2px dashed #e2e8f0', borderRadius: '16px', padding: '40px',
        textAlign: 'center', marginBottom: '24px', backgroundColor: '#f8fafc',
      }}>
        <div style={{ fontSize: '32px', marginBottom: '12px' }}>&#128228;</div>
        <div style={{ fontSize: '16px', fontWeight: 500, color: '#0f172a' }}>拖拽文件到此处上传</div>
        <div style={{ fontSize: '13px', color: '#94a3b8', marginTop: '8px' }}>支持 PDF/Word/Txt/Markdown/HTML/CSV/Excel/PPT/JSON/图片，单文件最大 100MB</div>
      </div>
      <Card style={{ padding: 0, overflow: 'hidden' }}>
        <table style={compStyles.table}>
          <thead><tr>
            <th style={compStyles.th}>文件名</th><th style={compStyles.th}>类型</th><th style={compStyles.th}>大小</th>
            <th style={compStyles.th}>状态</th><th style={compStyles.th}>上传时间</th><th style={compStyles.th}>操作</th>
          </tr></thead>
          <tbody>
            {documents.map((doc, i) => (
              <tr key={i}>
                <td style={compStyles.td}><span style={{ color: '#2563eb', cursor: 'pointer' }}>{doc.name}</span></td>
                <td style={compStyles.td}>{doc.type}</td>
                <td style={compStyles.td}>{doc.size}</td>
                <td style={compStyles.td}>
                  <Badge variant={doc.status === 'ready' ? 'success' : doc.status === 'error' ? 'error' : 'warning'}>
                    {doc.status === 'ready' ? '已就绪' : doc.status === 'error' ? '处理失败' : '处理中'}
                  </Badge>
                </td>
                <td style={compStyles.td}>{doc.uploaded}</td>
                <td style={compStyles.td}>
                  <div style={{ display: 'flex', gap: '4px' }}>
                    <Button variant="ghost" style={{ fontSize: '12px', padding: '4px 12px' }}>编辑</Button>
                    <Button variant="ghost" style={{ fontSize: '12px', padding: '4px 12px', color: '#dc2626' }}>删除</Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
    </div>
  );
}

// ========== SETTINGS SCREEN ==========
function SettingsScreen() {
  const [activeTab, setActiveTab] = React.useState('model');
  const tabs = [
    { key: 'model', label: 'AI 模型' }, { key: 'search', label: '搜索工具' },
    { key: 'status', label: '系统状态' },
  ];

  return (
    <div data-screen-label="06 Settings" style={{ padding: '32px 40px' }}>
      <h1 style={{ fontSize: '28px', fontWeight: 700, color: '#0f172a', margin: '0 0 24px', fontFamily: "'Space Grotesk', sans-serif", letterSpacing: '-0.02em' }}>系统配置</h1>
      <div style={{ display: 'flex', borderBottom: '1px solid #e2e8f0', marginBottom: '24px' }}>
        {tabs.map(t => <Tab key={t.key} label={t.label} active={activeTab === t.key} onClick={() => setActiveTab(t.key)} />)}
      </div>

      {activeTab === 'model' && (
        <div>
          <Card style={{ marginBottom: '12px' }}>
            <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 16px', fontFamily: "'Space Grotesk', sans-serif" }}>免费模型</h3>
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: '12px' }}>
              {['GPT-3.5 Turbo', 'Claude Haiku', '通义千问-Turbo', 'GLM-4-Flash'].map(m => (
                <div key={m} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px', border: '1px solid #e2e8f0', borderRadius: '12px' }}>
                  <div>
                    <div style={{ fontSize: '14px', fontWeight: 500, color: '#0f172a' }}>{m}</div>
                    <div style={{ fontSize: '12px', color: '#94a3b8', marginTop: '2px' }}>每月 100 次免费调用</div>
                  </div>
                  <Badge variant="success">可用</Badge>
                </div>
              ))}
            </div>
          </Card>
          <Card>
            <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 16px', fontFamily: "'Space Grotesk', sans-serif" }}>自定义模型</h3>
            <div style={{ marginBottom: '16px' }}>
              <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, color: '#475569', marginBottom: '6px' }}>API Key</label>
              <Input type="password" placeholder="sk-..." />
            </div>
            <div style={{ marginBottom: '20px' }}>
              <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, color: '#475569', marginBottom: '6px' }}>模型名称</label>
              <Input placeholder="gpt-4 / claude-3-opus / ..." />
            </div>
            <Button>保存配置</Button>
          </Card>
        </div>
      )}

      {activeTab === 'search' && (
        <div>
          <Card style={{ marginBottom: '12px' }}>
            <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 8px', fontFamily: "'Space Grotesk', sans-serif" }}>搜索工具配置</h3>
            <p style={{ fontSize: '13px', color: '#94a3b8', marginBottom: '16px' }}>
              配置搜索工具 API Key 后可使用深度模式，深度模式会在知识库无结果时自动联网搜索。
            </p>
            {[
              { name: 'Bing', desc: '微软搜索 · Azure 订阅获取', configured: true },
              { name: 'Tavily', desc: 'AI 搜索 · tavily.com 注册', configured: false },
              { name: '百度', desc: '中文搜索 · 百度开放平台', configured: false },
              { name: 'Google', desc: '谷歌搜索 · Google Cloud 订阅', configured: false },
            ].map((tool, i) => (
              <div key={i} style={{
                display: 'flex', justifyContent: 'space-between', alignItems: 'center',
                padding: '14px', border: '1px solid #e2e8f0', borderRadius: '12px',
                marginBottom: i < 3 ? '8px' : 0,
              }}>
                <div>
                  <div style={{ fontSize: '14px', fontWeight: 500, color: '#0f172a' }}>{tool.name}</div>
                  <div style={{ fontSize: '12px', color: '#94a3b8', marginTop: '2px' }}>{tool.desc}</div>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                  <Badge variant={tool.configured ? 'success' : 'neutral'}>{tool.configured ? '已配置' : '未配置'}</Badge>
                  <Button variant="secondary" style={{ fontSize: '12px', padding: '6px 14px' }}>
                    {tool.configured ? '修改' : '配置'}
                  </Button>
                </div>
              </div>
            ))}
          </Card>
          <Card>
            <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 8px', fontFamily: "'Space Grotesk', sans-serif" }}>添加搜索工具</h3>
            <div style={{ marginBottom: '16px' }}>
              <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, color: '#475569', marginBottom: '6px' }}>搜索引擎</label>
              <Select style={{ width: '100%' }}><option>Bing</option><option>Tavily</option><option>百度</option><option>Google</option></Select>
            </div>
            <div style={{ marginBottom: '20px' }}>
              <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, color: '#475569', marginBottom: '6px' }}>API Key</label>
              <Input type="password" placeholder="请输入 API Key" />
            </div>
            <Button>验证并保存</Button>
          </Card>
        </div>
      )}

      {activeTab === 'status' && (
        <div>
          <Card style={{ marginBottom: '12px' }}>
            <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 16px', fontFamily: "'Space Grotesk', sans-serif" }}>基础设施状态</h3>
            <p style={{ fontSize: '13px', color: '#94a3b8', marginBottom: '16px' }}>以下配置由管理员设置，普通用户不可修改。</p>
            <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px', backgroundColor: '#f8fafc', borderRadius: '12px' }}>
                <div>
                  <div style={{ fontSize: '14px', fontWeight: 500, color: '#0f172a' }}>向量数据库</div>
                  <div style={{ fontSize: '12px', color: '#94a3b8', marginTop: '2px' }}>PostgreSQL (pgvector)</div>
                </div>
                <Badge variant="success">运行中</Badge>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px', backgroundColor: '#f8fafc', borderRadius: '12px' }}>
                <div>
                  <div style={{ fontSize: '14px', fontWeight: 500, color: '#0f172a' }}>RAG 引擎</div>
                  <div style={{ fontSize: '12px', color: '#94a3b8', marginTop: '2px' }}>Eino Framework</div>
                </div>
                <Badge variant="success">运行中</Badge>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px', backgroundColor: '#f8fafc', borderRadius: '12px' }}>
                <div>
                  <div style={{ fontSize: '14px', fontWeight: 500, color: '#0f172a' }}>默认 AI 模型</div>
                  <div style={{ fontSize: '12px', color: '#94a3b8', marginTop: '2px' }}>GPT-4 (系统级)</div>
                </div>
                <Badge variant="blue">系统配置</Badge>
              </div>
            </div>
          </Card>

          <Card>
            <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 16px', fontFamily: "'Space Grotesk', sans-serif" }}>第三方平台集成</h3>
            <p style={{ fontSize: '13px', color: '#94a3b8', marginBottom: '16px' }}>集成配置由管理员管理，同步知识库可在知识库页面查看。</p>
            {[
              { name: '钉钉', desc: '通过 Webhook 同步知识库', status: '已连接', synced: '234 篇' },
              { name: '飞书', desc: '从飞书文档同步知识库', status: '已连接', synced: '189 篇' },
              { name: 'Notion', desc: '从 Notion 页面同步知识库', status: '未配置', synced: '-' },
            ].map((item, i) => (
              <div key={i} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px', border: '1px solid #e2e8f0', borderRadius: '12px', marginBottom: i < 2 ? '8px' : 0 }}>
                <div>
                  <div style={{ fontSize: '14px', fontWeight: 500, color: '#0f172a' }}>{item.name}</div>
                  <div style={{ fontSize: '12px', color: '#94a3b8', marginTop: '2px' }}>{item.desc}</div>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
                  <span style={{ fontSize: '12px', color: '#94a3b8' }}>已同步 {item.synced}</span>
                  <Badge variant={item.status === '已连接' ? 'success' : 'neutral'}>{item.status}</Badge>
                </div>
              </div>
            ))}
          </Card>
        </div>
      )}
    </div>
  );
}

// ========== ADMIN SCREEN ==========
function AdminScreen() {
  const [activeTab, setActiveTab] = React.useState('users');
  const users = [
    { name: '张三', email: 'zhangsan@company.com', role: '超级管理员', status: 'active', lastLogin: '在线' },
    { name: '李四', email: 'lisi@company.com', role: '管理员', status: 'active', lastLogin: '2 小时前' },
    { name: '王五', email: 'wangwu@company.com', role: '编辑者', status: 'active', lastLogin: '1 天前' },
    { name: '赵六', email: 'zhaoliu@company.com', role: '观察者', status: 'inactive', lastLogin: '1 周前' },
    { name: '孙七', email: 'sunqi@company.com', role: '编辑者', status: 'active', lastLogin: '3 天前' },
  ];
  const logs = [
    { time: '2024-01-15 14:32:01', level: 'INFO', module: 'Auth', message: '用户 zhangsan 登录成功' },
    { time: '2024-01-15 14:30:15', level: 'INFO', module: 'KB', message: '知识库「技术文档库」新增 5 篇文档' },
    { time: '2024-01-15 14:28:44', level: 'WARN', module: 'LLM', message: 'GPT-4 API 调用超时，已降级到 GPT-3.5' },
    { time: '2024-01-15 14:25:10', level: 'ERROR', module: 'Search', message: '联网搜索 API 返回 429 错误' },
    { time: '2024-01-15 14:20:00', level: 'INFO', module: 'Doc', message: '文档「架构设计.pdf」处理完成' },
  ];

  return (
    <div data-screen-label="07 Admin" style={{ padding: '32px 40px' }}>
      <h1 style={{ fontSize: '28px', fontWeight: 700, color: '#0f172a', margin: '0 0 24px', fontFamily: "'Space Grotesk', sans-serif", letterSpacing: '-0.02em' }}>后台管理</h1>
      <div style={{ display: 'flex', borderBottom: '1px solid #e2e8f0', marginBottom: '24px' }}>
        {[{ key: 'users', label: '用户管理' }, { key: 'sessions', label: '会话管理' }, { key: 'kb', label: '知识库管理' }, { key: 'logs', label: '系统日志' }, { key: 'vector', label: '向量数据库' }, { key: 'integration', label: '平台集成' }, { key: 'config', label: '配置管理' }].map(t => (
          <Tab key={t.key} label={t.label} active={activeTab === t.key} onClick={() => setActiveTab(t.key)} />
        ))}
      </div>

      {activeTab === 'users' && (
        <div>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '16px' }}>
            <SearchInput placeholder="搜索用户..." value="" onChange={() => {}} style={{ width: '320px' }} />
            <Button>+ 添加用户</Button>
          </div>
          <Card style={{ padding: 0, overflow: 'hidden' }}>
            <table style={compStyles.table}>
              <thead><tr><th style={compStyles.th}>用户</th><th style={compStyles.th}>邮箱</th><th style={compStyles.th}>角色</th><th style={compStyles.th}>状态</th><th style={compStyles.th}>最后登录</th><th style={compStyles.th}>操作</th></tr></thead>
              <tbody>
                {users.map((u, i) => (
                  <tr key={i}>
                    <td style={compStyles.td}><div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}><Avatar name={u.name} /><span style={{ fontWeight: 500 }}>{u.name}</span></div></td>
                    <td style={compStyles.td}>{u.email}</td>
                    <td style={compStyles.td}><Badge variant="blue">{u.role}</Badge></td>
                    <td style={compStyles.td}><Badge variant={u.status === 'active' ? 'success' : 'neutral'}>{u.status === 'active' ? '活跃' : '停用'}</Badge></td>
                    <td style={compStyles.td}>{u.lastLogin}</td>
                    <td style={compStyles.td}><Button variant="ghost" style={{ fontSize: '12px', padding: '4px 12px' }}>编辑</Button></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
        </div>
      )}

      {activeTab === 'logs' && (
        <div>
          <div style={{ display: 'flex', gap: '8px', marginBottom: '16px' }}>
            <Select style={{ width: '120px' }}><option>全部级别</option><option>ERROR</option><option>WARN</option><option>INFO</option></Select>
            <Select style={{ width: '120px' }}><option>全部模块</option><option>Auth</option><option>KB</option><option>LLM</option><option>Search</option><option>Doc</option></Select>
            <SearchInput placeholder="搜索日志..." value="" onChange={() => {}} style={{ width: '320px' }} />
          </div>
          <Card style={{ padding: 0, overflow: 'hidden' }}>
            <table style={compStyles.table}>
              <thead><tr><th style={compStyles.th}>时间</th><th style={compStyles.th}>级别</th><th style={compStyles.th}>模块</th><th style={compStyles.th}>消息</th></tr></thead>
              <tbody>
                {logs.map((log, i) => (
                  <tr key={i}>
                    <td style={{ ...compStyles.td, fontFamily: "'JetBrains Mono', monospace", fontSize: '12px' }}>{log.time}</td>
                    <td style={compStyles.td}><Badge variant={log.level === 'ERROR' ? 'error' : log.level === 'WARN' ? 'warning' : 'neutral'}>{log.level}</Badge></td>
                    <td style={compStyles.td}>{log.module}</td>
                    <td style={compStyles.td}>{log.message}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
        </div>
      )}

      {activeTab === 'sessions' && (
        <Card>
          <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 12px', fontFamily: "'Space Grotesk', sans-serif" }}>会话管理</h3>
          <p style={{ fontSize: '14px', color: '#94a3b8' }}>查看、搜索、删除历史会话。会话记录保留 90 天，超过自动归档。</p>
          <div style={{ display: 'flex', gap: '8px', marginTop: '16px' }}>
            <SearchInput placeholder="搜索会话..." value="" onChange={() => {}} style={{ flex: 1 }} />
            <Button variant="danger" style={{ fontSize: '12px' }}>清理过期会话</Button>
          </div>
        </Card>
      )}

      {activeTab === 'kb' && (
        <Card>
          <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 12px', fontFamily: "'Space Grotesk', sans-serif" }}>知识库全局管理</h3>
          <p style={{ fontSize: '14px', color: '#94a3b8' }}>查看所有租户的知识库状态，进行存储配额管理和异常排查。</p>
        </Card>
      )}

      {activeTab === 'vector' && (
        <Card>
          <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 8px', fontFamily: "'Space Grotesk', sans-serif" }}>向量数据库配置</h3>
          <p style={{ fontSize: '13px', color: '#94a3b8', marginBottom: '16px' }}>配置系统使用的向量数据库引擎，修改后需重启服务生效。</p>
          <div style={{ marginBottom: '16px' }}>
            <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, color: '#475569', marginBottom: '6px' }}>引擎类型</label>
            <Select style={{ width: '100%' }}><option>PostgreSQL (pgvector)</option><option>Elasticsearch</option><option>Milvus</option><option>Weaviate</option></Select>
          </div>
          <div style={{ marginBottom: '20px' }}>
            <label style={{ display: 'block', fontSize: '13px', fontWeight: 500, color: '#475569', marginBottom: '6px' }}>连接地址</label>
            <Input placeholder="postgresql://localhost:5432/solvify" />
          </div>
          <Button style={{ marginRight: '8px' }}>测试连接</Button>
          <Button variant="secondary">保存</Button>
        </Card>
      )}

      {activeTab === 'integration' && (
        <Card>
          <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 8px', fontFamily: "'Space Grotesk', sans-serif" }}>第三方平台集成</h3>
          <p style={{ fontSize: '13px', color: '#94a3b8', marginBottom: '16px' }}>配置钉钉、飞书、Notion 等平台的集成参数，实现知识库自动同步。</p>
          {[
            { name: '钉钉', desc: '通过 Webhook 同步知识库（需企业认证）', status: '已连接' },
            { name: '飞书', desc: '从飞书文档同步知识库（OAuth 授权）', status: '已连接' },
            { name: 'Notion', desc: '从 Notion 页面同步知识库（API Key）', status: '未配置' },
          ].map((item, i) => (
            <div key={i} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px', border: '1px solid #e2e8f0', borderRadius: '12px', marginBottom: i < 2 ? '8px' : 0 }}>
              <div>
                <div style={{ fontSize: '14px', fontWeight: 500, color: '#0f172a' }}>{item.name}</div>
                <div style={{ fontSize: '12px', color: '#94a3b8', marginTop: '2px' }}>{item.desc}</div>
              </div>
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
                <Badge variant={item.status === '已连接' ? 'success' : 'neutral'}>{item.status}</Badge>
                <Button variant="secondary" style={{ fontSize: '12px', padding: '6px 14px' }}>配置</Button>
              </div>
            </div>
          ))}
        </Card>
      )}

      {activeTab === 'config' && (
        <Card>
          <h3 style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', margin: '0 0 12px', fontFamily: "'Space Grotesk', sans-serif" }}>全局配置管理</h3>
          <p style={{ fontSize: '14px', color: '#94a3b8' }}>管理全局系统配置参数，包括存储配额、调用限制等。</p>
        </Card>
      )}
    </div>
  );
}

// ========== WIKI SCREEN ==========
function WikiScreen() {
  return (
    <div data-screen-label="08 Wiki" style={{ padding: '32px 40px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '24px' }}>
        <div>
          <h1 style={{ fontSize: '28px', fontWeight: 700, color: '#0f172a', margin: 0, fontFamily: "'Space Grotesk', sans-serif", letterSpacing: '-0.02em' }}>Wiki 知识库</h1>
          <p style={{ fontSize: '14px', color: '#94a3b8', margin: '8px 0 0' }}>由 Agent 自动生成的结构化知识页面</p>
        </div>
        <Button>生成 Wiki</Button>
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: '240px 1fr', gap: '16px' }}>
        <Card style={{ padding: '12px', height: 'fit-content' }}>
          <div style={{ fontSize: '12px', fontWeight: 600, color: '#94a3b8', marginBottom: '10px', padding: '0 10px', textTransform: 'uppercase', letterSpacing: '0.05em' }}>页面目录</div>
          {[
            { title: '系统架构', level: 0, active: true }, { title: '整体设计', level: 1 }, { title: '微服务拆分', level: 1 },
            { title: '数据库设计', level: 0 }, { title: 'ER 图', level: 1 }, { title: '索引优化', level: 1 },
            { title: 'API 文档', level: 0 }, { title: '认证接口', level: 1 }, { title: '知识库接口', level: 1 }, { title: '问答接口', level: 1 },
            { title: '部署指南', level: 0 }, { title: 'Docker 部署', level: 1 }, { title: 'K8s 部署', level: 1 },
          ].map((item, i) => (
            <div key={i} style={{
              padding: '6px 10px 6px ' + (10 + item.level * 16) + 'px',
              fontSize: '14px', cursor: 'pointer', borderRadius: '8px',
              color: item.active ? '#2563eb' : '#475569',
              backgroundColor: item.active ? '#eff6ff' : 'transparent',
              fontWeight: item.active ? 500 : 400, transition: 'all 0.15s ease',
            }}>{item.title}</div>
          ))}
        </Card>
        <Card>
          <h2 style={{ fontSize: '20px', fontWeight: 600, color: '#0f172a', margin: '0 0 16px', fontFamily: "'Space Grotesk', sans-serif" }}>系统架构</h2>
          <div style={{ fontSize: '14px', color: '#475569', lineHeight: 1.8 }}>
            <p>Solvify-Agent 采用微服务架构，核心组件包括：</p>
            <ul style={{ paddingLeft: '20px', margin: '12px 0' }}>
              <li><strong style={{ color: '#0f172a' }}>API Gateway</strong> - 统一入口，负责认证、限流、路由</li>
              <li><strong style={{ color: '#0f172a' }}>RAG Engine</strong> - 检索增强生成引擎，支持多模式搜索</li>
              <li><strong style={{ color: '#0f172a' }}>Agent Orchestrator</strong> - ReAct Agent 编排器，处理复杂推理任务</li>
              <li><strong style={{ color: '#0f172a' }}>Document Processor</strong> - 多格式文档解析和向量化</li>
              <li><strong style={{ color: '#0f172a' }}>Vector Store</strong> - 向量存储层，支持多种后端引擎</li>
            </ul>
            <p>各服务通过消息队列进行异步通信，保证系统的可扩展性和容错能力。</p>
            <div style={{ marginTop: '20px', padding: '16px', backgroundColor: '#f8fafc', borderRadius: '12px', border: '1px solid #e2e8f0' }}>
              <div style={{ fontSize: '12px', color: '#94a3b8', marginBottom: '8px', textTransform: 'uppercase', letterSpacing: '0.05em', fontWeight: 600 }}>知识图谱</div>
              <div style={{ fontSize: '13px', fontFamily: "'JetBrains Mono', monospace", color: '#475569', lineHeight: 1.8 }}>
                系统架构 → API Gateway, RAG Engine, Agent Orchestrator<br/>
                RAG Engine → 向量检索, 全文检索, 混合排序<br/>
                Agent Orchestrator → ReAct 循环, 工具调用, 多步推理
              </div>
            </div>
          </div>
        </Card>
      </div>
    </div>
  );
}

Object.assign(window, {
  LoginScreen, DashboardScreen, QAScreen, KnowledgeBaseScreen,
  DocumentsScreen, SettingsScreen, AdminScreen, WikiScreen,
});
