import { Collapsible } from '../ui/Collapsible';

interface TechnicalSectionProps {
  argv?: string[];
  sourceProbe?: string | null;
  outputProbe?: string | null;
}

function Block({ text }: { text: string }) {
  return (
    <pre className="max-h-96 overflow-auto rounded-lg bg-slate-950 p-3 font-mono text-[11px] leading-relaxed whitespace-pre-wrap text-slate-300">
      {text}
    </pre>
  );
}

export function TechnicalSection({ argv, sourceProbe, outputProbe }: TechnicalSectionProps) {
  return (
    <div className="space-y-2">
      <Collapsible title="ffmpeg argv">
        {argv && argv.length > 0 ? <Block text={argv.join(' \\\n  ')} /> : <p className="text-xs text-slate-500">No argv recorded.</p>}
      </Collapsible>
      <Collapsible title="ffprobe output (source)">
        {sourceProbe ? <Block text={sourceProbe} /> : <p className="text-xs text-slate-500">No probe stored.</p>}
      </Collapsible>
      <Collapsible title="ffprobe output (staged result)">
        {outputProbe ? <Block text={outputProbe} /> : <p className="text-xs text-slate-500">No probe stored.</p>}
      </Collapsible>
    </div>
  );
}
