import { Navigate } from 'react-router-dom';
import { useAuthStore } from '../store/auth';

export function AdminGuard({ children }: { children: React.ReactNode }) {
  const { user, loading, authEnabled } = useAuthStore();

  if (loading) {
    return (
      <div className="flex items-center justify-center h-screen">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-gray-900" />
      </div>
    );
  }

  if (authEnabled === false) {
    return <>{children}</>;
  }

  if (!user || user.role !== 'admin') {
    return <Navigate to="/" replace />;
  }

  return <>{children}</>;
}
