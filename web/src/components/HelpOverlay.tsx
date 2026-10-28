type Props = { onClose: () => void };

export function HelpOverlay({ onClose }: Props) {
  return (
    <div className="help-overlay" onClick={onClose} role="dialog" aria-label="Keyboard shortcuts">
      <div className="help-card" onClick={(e) => e.stopPropagation()}>
        <h2>Keyboard shortcuts</h2>
        <table>
          <tbody>
            <tr><td><kbd>j</kbd> / <kbd>k</kbd></td><td>next / prev trace or span</td></tr>
            <tr><td><kbd>Enter</kbd></td><td>open selection</td></tr>
            <tr><td><kbd>Esc</kbd></td><td>close detail → clear selection → blur</td></tr>
            <tr><td><kbd>/</kbd></td><td>focus search (traces or map find)</td></tr>
            <tr><td><kbd>e</kbd></td><td>errors-only filter (projects: open newest error)</td></tr>
            <tr><td><kbd>?</kbd></td><td>this help</td></tr>
            <tr><td><kbd>t</kbd></td><td>toggle theme</td></tr>
            <tr><td><kbd>l</kbd></td><td>toggle live tail</td></tr>
            <tr><td><kbd>←</kbd> / <kbd>→</kbd></td><td>fold / unfold subtree</td></tr>
            <tr><td><kbd>1</kbd>–<kbd>7</kbd></td><td>detail tabs</td></tr>
            <tr><td colSpan={2}><strong>Project (Monitor / Map)</strong></td></tr>
            <tr><td><kbd>1</kbd> / <kbd>m</kbd></td><td>Monitor tab</td></tr>
            <tr><td><kbd>2</kbd> / <kbd>g</kbd></td><td>Map (graph) tab</td></tr>
            <tr><td colSpan={2}><strong>Agent map</strong></td></tr>
            <tr><td><kbd>/</kbd></td><td>find node by name</td></tr>
            <tr><td><kbd>f</kbd></td><td>fit map to viewport</td></tr>
            <tr><td><kbd>r</kbd></td><td>toggle rare edges (&lt;1%)</td></tr>
            <tr><td>wheel / drag</td><td>zoom / pan</td></tr>
          </tbody>
        </table>
        <p className="hint" style={{ marginTop: 12 }}>
          Press Esc or click outside to close.
        </p>
      </div>
    </div>
  );
}
