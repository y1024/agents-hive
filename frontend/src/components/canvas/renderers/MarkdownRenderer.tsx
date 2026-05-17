import { Streamdown } from 'streamdown';
import { ALLOWED_TAGS, STREAMDOWN_PREVIEW_PLUGINS } from '../../../utils/streamdownConfig';

interface Props {
  content: string;
}

export function MarkdownRenderer({ content }: Props) {
  return (
    <div
      className="markdown-prose prose prose-sm max-w-none dark:prose-invert text-[var(--text-primary)] text-[13px] leading-[1.6] p-5 overflow-auto h-full"
    >
      <Streamdown plugins={STREAMDOWN_PREVIEW_PLUGINS} allowedTags={ALLOWED_TAGS}>
        {content}
      </Streamdown>
    </div>
  );
}
