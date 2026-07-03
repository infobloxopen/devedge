export const environment = {
  production: false,
  // Dev-only identity headers stamped onto same-origin API calls by
  // devAuthInterceptor, so a generated client round-trips against the
  // devedge-sdk dev authorizer — which reads raw account-id/groups metadata,
  // not a bearer token. Empty in production (environment.prod.ts), where real
  // OIDC + the bearer token take over. See the README "Local dev loop".
  devAuthHeaders: { 'account-id': 't1', groups: 'admin' } as Record<string, string>,
};
