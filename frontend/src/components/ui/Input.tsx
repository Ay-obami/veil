import { type InputHTMLAttributes } from "react";

interface Props extends InputHTMLAttributes<HTMLInputElement> {
  label?: string;
}

export function Input({ label, className = "", ...props }: Props) {
  return (
    <div className="flex flex-col gap-1">
      {label && (
        <label className="text-xs text-gray-400 font-medium">{label}</label>
      )}
      <input
        className={`bg-gray-800 border border-gray-700 rounded-lg px-3 py-2 text-sm text-gray-100 placeholder-gray-500 focus:outline-none focus:border-blue-500 ${className}`}
        {...props}
      />
    </div>
  );
}
