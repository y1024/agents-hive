import { create } from 'zustand';

// Canvas Artifact 类型定义
export type ArtifactType = 'html' | 'markdown' | 'json' | 'svg' | 'csv' | 'mermaid' | 'code' | 'ppt';

export interface Artifact {
  id: string;           // 唯一标识，自动生成
  title: string;        // 标签页标题（语言名或文件名）
  language: string;     // 代码语言标识
  content: string;      // 源码内容
  assetUri?: string;
  type: ArtifactType;
}

// 检测 Artifact 类型
export function detectArtifactType(lang: string, content: string): ArtifactType {
  const l = lang.toLowerCase();
  if (l === 'html' && /<!doctype|<html|<body/i.test(content)) return 'html';
  if (l === 'md' || l === 'markdown') return 'markdown';
  if (l === 'json') return 'json';
  if (l === 'svg' || (l === 'xml' && content.includes('<svg'))) return 'svg';
  if (l === 'csv') return 'csv';
  if (l === 'mermaid') return 'mermaid';
  return 'code';
}

interface CanvasState {
  open: boolean;
  artifacts: Artifact[];
  activeId: string | null;
  activeTab: 'preview' | 'code';
  // 操作
  openArtifact: (artifact: Artifact) => void;
  closeArtifact: (id: string) => void;
  setActiveId: (id: string) => void;
  setActiveTab: (tab: 'preview' | 'code') => void;
  closeAll: () => void;
}

const MAX_ARTIFACTS = 50;

export const useCanvasStore = create<CanvasState>()((set, get) => ({
  open: false,
  artifacts: [],
  activeId: null,
  activeTab: 'preview',

  openArtifact: (artifact) => {
    const { artifacts } = get();
    // 去重：同 title + language + content 才合并
    const existing = artifacts.find((a) =>
      a.title === artifact.title &&
      a.language === artifact.language &&
      (a.assetUri || '') === (artifact.assetUri || '') &&
      a.content.length === artifact.content.length &&
      a.content === artifact.content
    );
    if (existing) {
      set({ open: true, activeId: existing.id, activeTab: 'preview' });
      return;
    }
    // LRU：超出上限时淘汰最旧的
    const next = artifacts.length >= MAX_ARTIFACTS
      ? [...artifacts.slice(1), artifact]
      : [...artifacts, artifact];
    set({ open: true, artifacts: next, activeId: artifact.id, activeTab: 'preview' });
  },

  closeArtifact: (id) => {
    const { artifacts, activeId } = get();
    const next = artifacts.filter((a) => a.id !== id);
    if (next.length === 0) {
      set({ open: false, artifacts: [], activeId: null });
      return;
    }
    // 如果关闭的是当前激活的，切换到最后一个
    const newActiveId = activeId === id ? next[next.length - 1].id : activeId;
    set({ artifacts: next, activeId: newActiveId });
  },

  setActiveId: (id) => set({ activeId: id }),
  setActiveTab: (tab) => set({ activeTab: tab }),

  closeAll: () => set({ open: false, artifacts: [], activeId: null }),
}));
