import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { Tool, ToolHeader } from '../tool';

describe('ai-elements Tool theme integration', () => {
  it('uses Hive border token instead of inherited black border', () => {
    const { container } = render(<Tool />);

    const root = container.querySelector('[data-slot="collapsible"]');
    expect(root?.className).toContain('border-[var(--border-color)]');
    expect(root?.className).toContain('rounded-2xl');
  });

  it('uses Hive danger token for tool error state', () => {
    render(
      <Tool>
        <ToolHeader type="dynamic-tool" toolName="Shell" state="output-error" />
      </Tool>,
    );

    const badge = screen.getByText('Error').closest('[data-slot="badge"]');
    expect(badge?.className).toContain('bg-[var(--danger)]');
    expect(badge?.className).toContain('text-white');
    expect(badge?.querySelector('svg')?.className.baseVal).toContain('text-white');
  });
});
