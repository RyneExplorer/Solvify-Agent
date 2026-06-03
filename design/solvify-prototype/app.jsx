// Solvify-Agent Main App
// White-and-blue design v2

const TWEAK_DEFAULTS = /*EDITMODE-BEGIN*/{
  "showWiki": true
}/*EDITMODE-END*/;

const saved = JSON.parse(localStorage.getItem('solvify-tweaks-v2') || '{}');
const currentTweaks = { ...TWEAK_DEFAULTS, ...saved };

function applyTweak(key, value) {
  currentTweaks[key] = value;
  localStorage.setItem('solvify-tweaks-v2', JSON.stringify(currentTweaks));
  if (window.parent && window.parent !== window) {
    window.parent.postMessage({ type: '__edit_mode_set_keys', edits: { [key]: value } }, '*');
  }
}

function TweaksPanel({ tweaks, setTweaks, visible }) {
  if (!visible) return null;
  return (
    <div style={{
      position: 'fixed', bottom: 20, right: 20, padding: '16px',
      background: 'rgba(255,255,255,0.95)', color: '#0f172a', borderRadius: '16px',
      fontSize: '12px', display: 'grid', gap: '10px', minWidth: '200px',
      zIndex: 9999, backdropFilter: 'blur(12px)',
      border: '1px solid #e2e8f0', boxShadow: '0 4px 12px rgba(0,0,0,0.08)',
      fontFamily: "'Inter', sans-serif",
    }}>
      <div style={{ fontWeight: 600, fontSize: '13px', marginBottom: '4px', fontFamily: "'Space Grotesk', sans-serif" }}>Tweaks</div>
      <label style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ color: '#475569' }}>显示 Wiki</span>
        <input type="checkbox" checked={tweaks.showWiki} onChange={e => {
          const next = { ...tweaks, showWiki: e.target.checked };
          setTweaks(next);
          applyTweak('showWiki', e.target.checked);
        }} />
      </label>
    </div>
  );
}

function Sidebar({ activePage, onNavigate, tweaks }) {
  const navItems = [
    { key: 'dashboard', label: '概览', icon: (
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>
      </svg>
    )},
    { key: 'qa', label: '智能问答', icon: (
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
      </svg>
    )},
    { key: 'kb', label: '知识库', icon: (
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/>
      </svg>
    )},
    { key: 'docs', label: '文档', icon: (
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>
      </svg>
    )},
    { key: 'wiki', label: 'Wiki', icon: (
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="12" cy="12" r="10"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/><path d="M2 12h20"/>
      </svg>
    )},
    { key: 'settings', label: '配置', icon: (
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9c.26.604.852.997 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>
      </svg>
    )},
    { key: 'admin', label: '管理', icon: (
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
        <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>
      </svg>
    )},
  ];

  const filtered = tweaks.showWiki ? navItems : navItems.filter(n => n.key !== 'wiki');

  return (
    <div style={{
      width: '260px', backgroundColor: '#f8fafc', color: '#0f172a',
      height: '100vh', position: 'fixed', left: 0, top: 0,
      display: 'flex', flexDirection: 'column',
      borderRight: '1px solid #e2e8f0',
      fontFamily: "'Inter', sans-serif",
    }}>
      <div style={{
        padding: '16px 20px', display: 'flex', alignItems: 'center', gap: '10px',
        borderBottom: '1px solid #e2e8f0', height: '56px', boxSizing: 'border-box',
      }}>
        <div style={{
          width: '28px', height: '28px', borderRadius: '8px',
          backgroundColor: '#2563eb', color: '#ffffff',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: '14px', fontWeight: 700, fontFamily: "'Space Grotesk', sans-serif",
        }}>S</div>
        <span style={{ fontSize: '16px', fontWeight: 600, color: '#0f172a', fontFamily: "'Space Grotesk', sans-serif", letterSpacing: '-0.02em' }}>Solvify</span>
      </div>
      <div style={{ padding: '12px', flex: 1, overflowY: 'auto' }}>
        {filtered.map(item => (
          <div key={item.key} onClick={() => onNavigate(item.key)} style={{
            display: 'flex', alignItems: 'center', gap: '10px',
            padding: '10px 12px', borderRadius: '10px', cursor: 'pointer',
            fontSize: '14px', marginBottom: '2px',
            backgroundColor: activePage === item.key ? '#2563eb' : 'transparent',
            color: activePage === item.key ? '#ffffff' : '#475569',
            fontWeight: activePage === item.key ? 500 : 400,
            transition: 'all 0.15s ease',
          }}>
            <span style={{ width: '20px', textAlign: 'center', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>{item.icon}</span>
            {item.label}
          </div>
        ))}
      </div>
      <div style={{ padding: '12px 16px', borderTop: '1px solid #e2e8f0', display: 'flex', alignItems: 'center', gap: '10px' }}>
        <div style={{
          width: '28px', height: '28px', borderRadius: '50%',
          backgroundColor: '#2563eb', color: '#ffffff',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: '12px', fontWeight: 500,
        }}>A</div>
        <div>
          <div style={{ fontSize: '13px', fontWeight: 500, color: '#0f172a' }}>Admin</div>
          <div style={{ fontSize: '11px', color: '#94a3b8' }}>admin@solvify.ai</div>
        </div>
      </div>
    </div>
  );
}

function Header({ user }) {
  return (
    <div style={{
      height: '56px', backgroundColor: '#ffffff',
      display: 'flex', alignItems: 'center', padding: '0 24px',
      borderBottom: '1px solid #e2e8f0', position: 'sticky', top: 0, zIndex: 100,
      fontFamily: "'Inter', sans-serif", marginLeft: '260px',
    }}>
      <div style={{ flex: 1 }} />
      <div style={{ display: 'flex', alignItems: 'center', gap: '12px' }}>
        <button style={{
          background: 'transparent', border: 'none', color: '#94a3b8',
          cursor: 'pointer', padding: '6px', borderRadius: '8px',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
        }}>
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
            <path d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 0 1-3.46 0"/>
          </svg>
        </button>
        <div style={{
          width: '28px', height: '28px', borderRadius: '50%',
          backgroundColor: '#2563eb', color: '#ffffff',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          fontSize: '12px', fontWeight: 500, cursor: 'pointer',
        }}>{user.name.charAt(0)}</div>
      </div>
    </div>
  );
}

function App() {
  const [loggedIn, setLoggedIn] = React.useState(false);
  const [user, setUser] = React.useState({ name: 'Admin', email: 'admin@solvify.ai' });
  const [activePage, setActivePage] = React.useState('dashboard');
  const [tweaks, setTweaks] = React.useState(currentTweaks);
  const [showTweaks, setShowTweaks] = React.useState(false);

  React.useEffect(() => {
    window.addEventListener('message', (e) => {
      if (e.data?.type === '__activate_edit_mode') setShowTweaks(true);
      if (e.data?.type === '__deactivate_edit_mode') setShowTweaks(false);
    });
    window.parent.postMessage({ type: '__edit_mode_available' }, '*');
  }, []);

  const handleLogin = (userData) => { setUser(userData); setLoggedIn(true); };

  if (!loggedIn) return <LoginScreen onLogin={handleLogin} />;

  const renderPage = () => {
    switch (activePage) {
      case 'dashboard': return <DashboardScreen user={user} />;
      case 'qa': return <QAScreen />;
      case 'kb': return <KnowledgeBaseScreen />;
      case 'docs': return <DocumentsScreen />;
      case 'wiki': return <WikiScreen />;
      case 'settings': return <SettingsScreen />;
      case 'admin': return <AdminScreen />;
      default: return <DashboardScreen user={user} />;
    }
  };

  return (
    <div style={{ fontFamily: "'Inter', sans-serif", backgroundColor: '#ffffff', minHeight: '100vh' }}>
      <Sidebar activePage={activePage} onNavigate={setActivePage} tweaks={tweaks} />
      <div style={{ marginLeft: '260px' }}>
        <Header user={user} />
        <div style={{ minHeight: 'calc(100vh - 56px)', backgroundColor: '#ffffff' }}>
          {renderPage()}
        </div>
      </div>
      <TweaksPanel tweaks={tweaks} setTweaks={setTweaks} visible={showTweaks} />
    </div>
  );
}

ReactDOM.createRoot(document.getElementById('root')).render(<App />);
