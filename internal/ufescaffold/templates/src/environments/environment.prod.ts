export const environment = {
  production: true,
  // No dev identity headers in production: real OIDC supplies the bearer token
  // and the dev authorizer is replaced by a real policy. devAuthInterceptor is
  // a no-op when this is empty.
  devAuthHeaders: {} as Record<string, string>,
};
