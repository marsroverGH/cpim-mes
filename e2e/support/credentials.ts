function required(name: 'E2E_ADMIN_USERNAME' | 'E2E_ADMIN_PASSWORD'): string {
  const value = process.env[name]
  if (value === undefined || value.length === 0 || (name === 'E2E_ADMIN_USERNAME' && value.trim().length === 0)) {
    throw new Error(`${name} is required; E2E has no built-in credentials`)
  }
  return value
}

export const E2E_ADMIN_USERNAME = required('E2E_ADMIN_USERNAME')
export const E2E_ADMIN_PASSWORD = required('E2E_ADMIN_PASSWORD')
