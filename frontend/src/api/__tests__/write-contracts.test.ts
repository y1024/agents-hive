import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const SOURCE_ROOT = resolve(__dirname, '../..');

const files = [
  'api/node-client.ts',
  'pages/admin/LLMProviders.tsx',
  'components/settings/ExternalResourcesSettings.tsx',
] as const;

const forbiddenBroadWriteTypes = [
  /Partial<\s*AdminProvider\s*>/g,
  /Partial<\s*LLMProviderRecord\s*>/g,
  /Partial<\s*LLMModelRecord\s*>/g,
  /Partial<\s*ExternalResource\s*>/g,
];

describe('frontend write DTO contracts', () => {
  it.each(files)('%s does not use broad record Partial types for writes', (file) => {
    const source = readFileSync(resolve(SOURCE_ROOT, file), 'utf-8');

    for (const pattern of forbiddenBroadWriteTypes) {
      expect(source.match(pattern) ?? [], `${file} contains ${pattern}`).toHaveLength(0);
    }
  });

  it('external resources writes go through the explicit upsert request API', () => {
    const source = readFileSync(resolve(SOURCE_ROOT, 'components/settings/ExternalResourcesSettings.tsx'), 'utf-8');

    expect(source).toContain('ExternalResourceSaveRequest');
    expect(source).toContain('upsertExternalResource(resource)');
    expect(source).not.toContain('saveExternalResource(resource)');
  });

  it('LLM provider page submits explicit create and update DTO types', () => {
    const source = readFileSync(resolve(SOURCE_ROOT, 'pages/admin/LLMProviders.tsx'), 'utf-8');

    expect(source).toContain('LLMProviderCreateRequest');
    expect(source).toContain('LLMProviderUpdateRequest');
    expect(source).toContain('LLMModelCreateRequest');
    expect(source).toContain('LLMModelUpdateRequest');
  });
});
