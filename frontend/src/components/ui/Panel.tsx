import React from 'react';

interface PanelProps {
  id?: string;
  title: React.ReactNode;
  right?: React.ReactNode;
  noPad?: boolean;
  onClose?: () => void;
  children: React.ReactNode;
}

export function Panel({ id, title, right, noPad, onClose, children }: PanelProps) {
  return (
    <div id={id} className="panel" data-panel-id={id}>
      <div className="panel-head">
        <span className="panel-title">{title}</span>
        <div className="panel-right">
          {right}
          {onClose && (
            <button className="panel-close" onClick={onClose} aria-label="Close panel">×</button>
          )}
        </div>
      </div>
      <div className={`panel-body${noPad ? '' : ' pad'}`}>{children}</div>
    </div>
  );
}
