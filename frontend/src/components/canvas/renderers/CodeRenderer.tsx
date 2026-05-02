import { CodeBlock } from '@/components/ai-elements/code-block';

interface Props {
  content: string;
  language: string;
}

function sanitizeLanguage(lang: string): string {
  return lang.replace(/[^a-zA-Z0-9+#-]/g, '');
}

export function CodeRenderer({ content, language }: Props) {
  const safeLang = sanitizeLanguage(language) || 'text';
  return (
    <div className="h-full overflow-auto">
      <CodeBlock
        code={content}
        language={safeLang}
        showLineNumbers
      />
    </div>
  );
}
