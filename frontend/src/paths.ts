const basePath = '/R2RProcessControl';

export function appPath(path: string): string {
  const normalizedPath = path.startsWith('/') ? path : `/${path}`;
  return `${basePath}${normalizedPath}`;
}
