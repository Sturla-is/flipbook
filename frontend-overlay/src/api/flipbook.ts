import api from './client';

export interface FlipbookItem {
  id: string;
  title: string;
  slug: string;
  status: string;
  pageCount: number;
  viewerUrl: string;
  embedUrl: string;
  thumbnailUrl?: string;
  createdAt: string;
}

export interface FlipbookDetail extends FlipbookItem {
  width: number;
  height: number;
  embedCode: string;
  pages: string[];
  error: string;
}

export interface UsageInfo {
  pagesUsed: number;
  pagesLimit: number;
  planName: string;
}

export const flipbookApi = {
  list: async (): Promise<{ flipbooks: FlipbookItem[] }> => {
    const { data } = await api.get('/saas/flipbooks');
    return data;
  },

  get: async (id: string): Promise<FlipbookDetail> => {
    const { data } = await api.get(`/saas/flipbooks/${id}`);
    return data;
  },

  upload: async (file: File, title?: string, onProgress?: (pct: number) => void): Promise<{ id: string; slug: string; statusUrl: string }> => {
    const formData = new FormData();
    formData.append('file', file);
    if (title) formData.append('title', title);

    const { data } = await api.post('/saas/flipbooks', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
      onUploadProgress: (e) => {
        if (onProgress && e.total) {
          onProgress(Math.round((e.loaded / e.total) * 100));
        }
      },
    });
    return data;
  },

  update: async (id: string, body: { title?: string; description?: string }): Promise<void> => {
    await api.put(`/saas/flipbooks/${id}`, body);
  },

  delete: async (id: string): Promise<void> => {
    await api.delete(`/saas/flipbooks/${id}`);
  },

  usage: async (): Promise<UsageInfo> => {
    const { data } = await api.get('/saas/usage');
    return data;
  },

  // Public status endpoint (no auth)
  status: async (id: string): Promise<{ id: string; status: string; slug: string; pageCount: number; error: string }> => {
    const { data } = await api.get(`/flipbooks/${id}/status`);
    return data;
  },
};
