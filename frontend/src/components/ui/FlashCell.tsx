import { useState, useEffect, useRef } from 'react';

interface FlashCellProps {
  value: number;
  formatter?: (v: number) => string;
  className?: string;
}

export function FlashCell({ value, formatter, className = '' }: FlashCellProps) {
  const prevRef = useRef<number>(value);
  const [flashKey, setFlashKey] = useState(0);

  useEffect(() => {
    if (prevRef.current !== value) {
      setFlashKey(k => k + 1);
      prevRef.current = value;
    }
  }, [value]);

  return (
    <span key={flashKey} className={`flash ${className}`}>
      {formatter ? formatter(value) : String(value)}
    </span>
  );
}
