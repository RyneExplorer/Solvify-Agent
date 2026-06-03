// Solvify-Agent Shared Components
// ChatGPT-inspired white-and-black design system v2

const compStyles = {
  // Buttons
  btnPrimary: {
    backgroundColor: '#000000',
    color: '#ffffff',
    border: 'none',
    borderRadius: '12px',
    padding: '10px 20px',
    fontSize: '14px',
    fontWeight: 500,
    cursor: 'pointer',
    display: 'inline-flex',
    alignItems: 'center',
    gap: '6px',
    lineHeight: '20px',
    transition: 'all 0.15s ease',
    fontFamily: "'Inter', sans-serif",
  },
  btnSecondary: {
    backgroundColor: '#ffffff',
    color: '#000000',
    border: '1px solid #e5e5e5',
    borderRadius: '12px',
    padding: '10px 20px',
    fontSize: '14px',
    fontWeight: 400,
    cursor: 'pointer',
    display: 'inline-flex',
    alignItems: 'center',
    gap: '6px',
    lineHeight: '20px',
    transition: 'all 0.15s ease',
    fontFamily: "'Inter', sans-serif",
  },
  btnDanger: {
    backgroundColor: '#dc2626',
    color: '#ffffff',
    border: 'none',
    borderRadius: '12px',
    padding: '10px 20px',
    fontSize: '14px',
    fontWeight: 500,
    cursor: 'pointer',
    display: 'inline-flex',
    alignItems: 'center',
    gap: '6px',
    lineHeight: '20px',
    fontFamily: "'Inter', sans-serif",
  },
  btnGhost: {
    backgroundColor: 'transparent',
    color: '#4a4a4a',
    border: 'none',
    borderRadius: '12px',
    padding: '8px 16px',
    fontSize: '14px',
    fontWeight: 400,
    cursor: 'pointer',
    display: 'inline-flex',
    alignItems: 'center',
    gap: '6px',
    lineHeight: '20px',
    transition: 'all 0.15s ease',
    fontFamily: "'Inter', sans-serif",
  },
  btnIcon: {
    backgroundColor: 'transparent',
    color: '#8e8e8e',
    border: 'none',
    borderRadius: '8px',
    padding: '8px',
    fontSize: '16px',
    cursor: 'pointer',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    transition: 'all 0.15s ease',
  },
  // Input
  input: {
    backgroundColor: '#f7f7f8',
    border: '1px solid #e5e5e5',
    borderRadius: '12px',
    padding: '12px 16px',
    fontSize: '14px',
    lineHeight: '20px',
    width: '100%',
    boxSizing: 'border-box',
    outline: 'none',
    color: '#000000',
    transition: 'border-color 0.15s ease',
    fontFamily: "'Inter', sans-serif",
  },
  inputFocus: {
    borderColor: '#000000',
  },
  textarea: {
    backgroundColor: '#f7f7f8',
    border: '1px solid #e5e5e5',
    borderRadius: '12px',
    padding: '12px 16px',
    fontSize: '14px',
    lineHeight: '20px',
    width: '100%',
    boxSizing: 'border-box',
    outline: 'none',
    resize: 'vertical',
    color: '#000000',
    fontFamily: "'Inter', sans-serif",
  },
  // Select
  select: {
    backgroundColor: '#f7f7f8',
    border: '1px solid #e5e5e5',
    borderRadius: '12px',
    padding: '10px 36px 10px 16px',
    fontSize: '14px',
    lineHeight: '20px',
    cursor: 'pointer',
    appearance: 'none',
    color: '#000000',
    outline: 'none',
    fontFamily: "'Inter', sans-serif",
    backgroundImage: 'url("data:image/svg+xml,%3Csvg xmlns=\'http://www.w3.org/2000/svg\' width=\'12\' height=\'12\' viewBox=\'0 0 12 12\'%3E%3Cpath fill=\'%238e8e8e\' d=\'M6 8.825L1.175 4 2.238 2.938 6 6.7 9.763 2.937 10.825 4z\'/%3E%3C/svg%3E")',
    backgroundRepeat: 'no-repeat',
    backgroundPosition: 'right 12px center',
  },
  // Card
  card: {
    backgroundColor: '#ffffff',
    border: '1px solid #e5e5e5',
    borderRadius: '16px',
    padding: '20px',
  },
  // Badge
  badgeSuccess: {
    backgroundColor: '#dcfce7',
    color: '#16a34a',
    borderRadius: '9999px',
    padding: '3px 10px',
    fontSize: '12px',
    fontWeight: 500,
    display: 'inline-block',
    fontFamily: "'Inter', sans-serif",
  },
  badgeWarning: {
    backgroundColor: '#fef3c7',
    color: '#d97706',
    borderRadius: '9999px',
    padding: '3px 10px',
    fontSize: '12px',
    fontWeight: 500,
    display: 'inline-block',
    fontFamily: "'Inter', sans-serif",
  },
  badgeError: {
    backgroundColor: '#fee2e2',
    color: '#dc2626',
    borderRadius: '9999px',
    padding: '3px 10px',
    fontSize: '12px',
    fontWeight: 500,
    display: 'inline-block',
    fontFamily: "'Inter', sans-serif",
  },
  badgeNeutral: {
    backgroundColor: '#f5f5f5',
    color: '#4a4a4a',
    borderRadius: '9999px',
    padding: '3px 10px',
    fontSize: '12px',
    fontWeight: 500,
    display: 'inline-block',
    fontFamily: "'Inter', sans-serif",
  },
  badgeBlue: {
    backgroundColor: '#dbeafe',
    color: '#2563eb',
    borderRadius: '9999px',
    padding: '3px 10px',
    fontSize: '12px',
    fontWeight: 500,
    display: 'inline-block',
    fontFamily: "'Inter', sans-serif",
  },
  // Table
  table: {
    width: '100%',
    borderCollapse: 'collapse',
    fontSize: '14px',
    fontFamily: "'Inter', sans-serif",
  },
  th: {
    backgroundColor: '#f7f7f8',
    borderBottom: '1px solid #e5e5e5',
    padding: '12px 16px',
    textAlign: 'left',
    fontWeight: 500,
    fontSize: '12px',
    color: '#8e8e8e',
    textTransform: 'uppercase',
    letterSpacing: '0.05em',
  },
  td: {
    borderBottom: '1px solid #f0f0f0',
    padding: '12px 16px',
    color: '#000000',
  },
  // Avatar
  avatar: {
    width: '32px',
    height: '32px',
    borderRadius: '50%',
    backgroundColor: '#000000',
    color: '#ffffff',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontSize: '14px',
    fontWeight: 500,
    fontFamily: "'Inter', sans-serif",
  },
  // Tabs
  tab: {
    padding: '10px 16px',
    fontSize: '14px',
    color: '#8e8e8e',
    cursor: 'pointer',
    background: 'none',
    border: 'none',
    borderBottom: '2px solid transparent',
    transition: 'all 0.15s ease',
    fontFamily: "'Inter', sans-serif",
  },
  tabActive: {
    color: '#000000',
    fontWeight: 500,
    borderBottomColor: '#000000',
  },
};

// Button component
function Button({ children, variant = 'primary', onClick, style = {}, disabled, ...props }) {
  const baseStyle = variant === 'primary' ? compStyles.btnPrimary
    : variant === 'danger' ? compStyles.btnDanger
    : variant === 'ghost' ? compStyles.btnGhost
    : variant === 'icon' ? compStyles.btnIcon
    : compStyles.btnSecondary;
  return (
    <button
      style={{ ...baseStyle, ...(disabled ? { opacity: 0.4, cursor: 'not-allowed' } : {}), ...style }}
      onClick={disabled ? undefined : onClick}
      {...props}
    >
      {children}
    </button>
  );
}

// Input component
function Input({ style = {}, ...props }) {
  const [focused, setFocused] = React.useState(false);
  return (
    <input
      style={{ ...compStyles.input, ...(focused ? compStyles.inputFocus : {}), ...style }}
      onFocus={() => setFocused(true)}
      onBlur={() => setFocused(false)}
      {...props}
    />
  );
}

// Select component
function Select({ children, style = {}, ...props }) {
  return (
    <select style={{ ...compStyles.select, ...style }} {...props}>
      {children}
    </select>
  );
}

// Badge component
function Badge({ children, variant = 'neutral' }) {
  const style = variant === 'success' ? compStyles.badgeSuccess
    : variant === 'warning' ? compStyles.badgeWarning
    : variant === 'error' ? compStyles.badgeError
    : variant === 'blue' ? compStyles.badgeBlue
    : compStyles.badgeNeutral;
  return <span style={style}>{children}</span>;
}

// Card component
function Card({ children, style = {}, ...props }) {
  return <div style={{ ...compStyles.card, ...style }} {...props}>{children}</div>;
}

// Avatar component
function Avatar({ name }) {
  const initial = name ? name.charAt(0).toUpperCase() : '?';
  return <div style={compStyles.avatar}>{initial}</div>;
}

// Tab component
function Tab({ label, active, onClick }) {
  return (
    <button
      style={{ ...compStyles.tab, ...(active ? compStyles.tabActive : {}) }}
      onClick={onClick}
    >
      {label}
    </button>
  );
}

// Stat Card
function StatCard({ label, value, icon, color }) {
  return (
    <Card style={{ display: 'flex', alignItems: 'center', gap: '16px' }}>
      <div style={{
        width: '48px', height: '48px', borderRadius: '12px',
        backgroundColor: '#f7f7f8',
        display: 'flex', alignItems: 'center', justifyContent: 'center',
        fontSize: '20px',
      }}>
        {icon}
      </div>
      <div>
        <div style={{ fontSize: '28px', fontWeight: 700, color: '#000000', lineHeight: 1.2, fontFamily: "'Space Grotesk', sans-serif" }}>{value}</div>
        <div style={{ fontSize: '13px', color: '#8e8e8e', marginTop: '4px' }}>{label}</div>
      </div>
    </Card>
  );
}

// Search Input with icon
function SearchInput({ placeholder, value, onChange, style = {} }) {
  const [focused, setFocused] = React.useState(false);
  return (
    <div style={{ position: 'relative', ...style }}>
      <span style={{
        position: 'absolute', left: '14px', top: '50%', transform: 'translateY(-50%)',
        color: '#8e8e8e', fontSize: '14px', pointerEvents: 'none',
      }}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>
        </svg>
      </span>
      <input
        style={{
          ...compStyles.input,
          paddingLeft: '40px',
          ...(focused ? compStyles.inputFocus : {}),
        }}
        placeholder={placeholder}
        value={value}
        onChange={onChange}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
      />
    </div>
  );
}

Object.assign(window, {
  Button, Input, Select, Badge, Card, Avatar, Tab, StatCard, SearchInput, compStyles,
});
