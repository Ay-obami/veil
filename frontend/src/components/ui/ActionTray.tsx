/**
 * ActionTray — live progress cards for long-running actions.
 *
 * A caller starts a job with a fixed list of step labels, then drives it from
 * real promises: advance() as each step completes, detail() for sub-status on
 * the active step, and exactly one terminal call — finish() or fail().
 *
 * Successful jobs self-dismiss; failed ones stay until dismissed so the error
 * is still readable after the fact.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';

const MAX_VISIBLE = 3;
const SUCCESS_DISMISS_MS = 8000;

export type StepStatus = 'pending' | 'active' | 'done' | 'failed' | 'cancelled';

interface Step {
  label: string;
  status: StepStatus;
  /** Live sub-status for the active step, e.g. "attempt 4/30". */
  detail?: string;
  startedAt: number | null;
  endedAt: number | null;
}

interface Job {
  id: string;
  title: string;
  steps: Step[];
  summary?: Record<string, string>;
  error?: string;
  startedAt: number;
  completedAt?: number;
}

/**
 * The handle a caller drives. Hooks accept this as `report` so they can push
 * progress without knowing the tray exists.
 */
export interface JobHandle {
  id: string;
  /** Mark the active step done and start the next one. */
  advance: () => void;
  /** Set sub-status text on the active step. */
  detail: (text: string) => void;
  /**
   * Mark the active step failed but leave the job running — for batches where
   * one item failing doesn't stop the rest. Terminate with finish() as usual;
   * failed steps survive it and the card reports a partial result.
   */
  failStep: (reason?: string) => void;
  /** Mark every remaining step done, optionally attaching a summary. */
  finish: (opts?: { summary?: Record<string, string> }) => void;
  /** Mark the active step failed and cancel the rest. */
  fail: (opts: { message: string }) => void;
}

/** The subset a hook needs — lets hooks stay decoupled from the tray. */
export type StepReporter = Pick<JobHandle, 'advance' | 'detail'>;

interface TrayContextValue {
  start: (job: { title: string; steps: string[] }) => JobHandle;
  dismiss: (id: string) => void;
  jobs: Job[];
}

const TrayContext = createContext<TrayContextValue>({
  start: () => ({
    id: '',
    advance: () => {},
    detail: () => {},
    failStep: () => {},
    finish: () => {},
    fail: () => {},
  }),
  dismiss: () => {},
  jobs: [],
});

export function useTray() {
  return useContext(TrayContext);
}

let nextJobId = 0;

export function TrayProvider({ children }: { children: ReactNode }) {
  const [jobs, setJobs] = useState<Job[]>([]);
  const timers = useRef<number[]>([]);

  useEffect(() => {
    const pending = timers.current;
    return () => pending.forEach(clearTimeout);
  }, []);

  const dismiss = useCallback((id: string) => {
    setJobs(prev => prev.filter(j => j.id !== id));
  }, []);

  const start = useCallback(
    ({ title, steps }: { title: string; steps: string[] }): JobHandle => {
      const id = `job_${nextJobId++}`;
      const now = Date.now();

      setJobs(prev => [
        ...prev,
        {
          id,
          title,
          startedAt: now,
          steps: steps.map((label, i) => ({
            label,
            status: i === 0 ? 'active' : 'pending',
            startedAt: i === 0 ? now : null,
            endedAt: null,
          })),
        },
      ]);

      // Step cursor lives in the closure — the caller drives it, and no render
      // depends on it, so a ref would only add noise.
      //
      // Every updater below reads `at`, a snapshot of the cursor taken when the
      // method is CALLED. Reading `cursor` inside an updater would instead see
      // its value when React flushes, which for calls batched into one tick is
      // whatever the last call left behind — so a failStep()/advance() pair
      // would land on the wrong step.
      let cursor = 0;
      const update = (mut: (j: Job) => Job) =>
        setJobs(prev => prev.map(j => (j.id === id ? mut(j) : j)));

      const advance = () => {
        const at = (cursor += 1);
        update(j => ({
          ...j,
          steps: j.steps.map((s, i) => {
            if (i < at) {
              // 'failed' is terminal — a step marked by failStep() must survive
              // the batch advancing past it, or the card would claim success.
              if (s.status === 'done' || s.status === 'failed') return s;
              return { ...s, status: 'done', detail: undefined, endedAt: s.endedAt ?? Date.now() };
            }
            if (i === at) return { ...s, status: 'active', startedAt: Date.now() };
            return s;
          }),
        }));
      };

      const detail = (text: string) => {
        const at = cursor;
        update(j => ({
          ...j,
          steps: j.steps.map((s, i) => (i === at ? { ...s, detail: text } : s)),
        }));
      };

      const failStep = (reason?: string) => {
        const at = cursor;
        update(j => ({
          ...j,
          steps: j.steps.map((s, i) =>
            i === at ? { ...s, status: 'failed', detail: reason, endedAt: Date.now() } : s,
          ),
        }));
      };

      const finish = ({ summary }: { summary?: Record<string, string> } = {}) => {
        update(j => ({
          ...j,
          // Steps marked failed via failStep() stay failed — the job completed,
          // but not every part of it succeeded.
          steps: j.steps.map(s =>
            s.status === 'failed'
              ? s
              : { ...s, status: 'done', detail: undefined, endedAt: s.endedAt ?? Date.now() },
          ),
          summary: summary ?? j.summary,
          completedAt: Date.now(),
        }));
        timers.current.push(
          setTimeout(() => dismiss(id), SUCCESS_DISMISS_MS) as unknown as number,
        );
      };

      const fail = ({ message }: { message: string }) => {
        const at = cursor;
        update(j => ({
          ...j,
          steps: j.steps.map((s, i) => {
            if (i < at) return s;
            if (i === at)
              return { ...s, status: 'failed', detail: undefined, endedAt: Date.now() };
            return { ...s, status: 'cancelled' };
          }),
          error: message,
          completedAt: Date.now(),
        }));
      };

      return { id, advance, detail, failStep, finish, fail };
    },
    [dismiss],
  );

  return (
    <TrayContext.Provider value={{ start, dismiss, jobs }}>
      {children}
      <div className="tray">
        {jobs.slice(-MAX_VISIBLE).map(j => (
          <TrayCard key={j.id} job={j} onDismiss={() => dismiss(j.id)} />
        ))}
      </div>
    </TrayContext.Provider>
  );
}

/** Ticks once a second while `active`, so elapsed time visibly moves. */
function useTick(active: boolean): number {
  const [, setTick] = useState(0);
  useEffect(() => {
    if (!active) return;
    const h = setInterval(() => setTick(t => t + 1), 1000);
    return () => clearInterval(h);
  }, [active]);
  return Date.now();
}

function secs(ms: number): string {
  return `${(ms / 1000).toFixed(1)}S`;
}

function TrayCard({ job, onDismiss }: { job: Job; onDismiss: () => void }) {
  const running = !job.completedAt;
  const now = useTick(running);
  const failed = !!job.error;
  // Completed, but some step failed along the way (a batch that carried on).
  const partial = !running && !failed && job.steps.some(s => s.status === 'failed');

  const state = failed ? 'failed' : running ? 'running' : partial ? 'partial' : 'done';
  const mark = failed ? '×' : running ? '▸' : partial ? '!' : '✓';

  return (
    <div className={`tray-card ${state}`}>
      <div className="tray-head">
        <span className="tray-mark">{mark}</span>
        <span className="tray-title">{job.title}</span>
        <span className="tray-elapsed">
          {secs((job.completedAt ?? now) - job.startedAt)}
        </span>
        <button className="tray-x" onClick={onDismiss} aria-label="Dismiss">
          ×
        </button>
      </div>

      <div className="tray-steps">
        {job.steps.map((s, i) => (
          <div key={i} className={`tray-step ${s.status}`}>
            <span className="ind" />
            <span className="lbl">
              {s.label}
              {s.detail && <span className="det"> · {s.detail}</span>}
            </span>
            <span className="t">
              {s.status === 'active' && s.startedAt && secs(now - s.startedAt)}
              {s.status === 'done' && s.startedAt && s.endedAt && secs(s.endedAt - s.startedAt)}
            </span>
          </div>
        ))}
      </div>

      {failed && <div className="tray-error">{job.error}</div>}

      {!failed && !running && job.summary && (
        <div className="tray-summary">
          {Object.entries(job.summary).map(([k, v]) => (
            <div key={k} className="row">
              <span className="k">{k}</span>
              <span className="v">{v}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
