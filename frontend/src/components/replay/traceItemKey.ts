import type { TraceTimelineItem } from '../../types/api';

export function traceItemKey(item: TraceTimelineItem, index: number): string {
  if (item.kind === 'quality_event') {
    const qualityEvent = traceQualityEvent(item);
    const attrs = qualityEvent?.attributes;
    const attrToolCallID = attrs && typeof attrs.tool_call_id === 'string' ? attrs.tool_call_id : '';
    return [
      'quality',
      qualityEvent?.turn_id || item.trace_id || 'trace',
      qualityEvent?.name || item.operation || item.kind,
      qualityEvent?.span_id || item.span_id || attrToolCallID || String(index),
      qualityEvent?.case_id || attrToolCallID || '',
    ].join(':');
  }
  return ['span', item.trace_id || 'trace', item.span_id || String(index), item.operation || item.kind].join(':');
}

function traceQualityEvent(item: TraceTimelineItem) {
  const raw = item.attributes?.quality_event;
  if (!raw) return null;
  if (typeof raw === 'string') {
    try {
      return JSON.parse(raw);
    } catch {
      return null;
    }
  }
  if (typeof raw === 'object') return raw;
  return null;
}
