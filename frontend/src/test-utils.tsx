import { render as rtlRender } from '@testing-library/react'
import type { ReactElement } from 'react'
import { ConfigProvider } from 'antd'

export * from '@testing-library/react'

export function render(ui: ReactElement, options?: Parameters<typeof rtlRender>[1]) {
  return rtlRender(
    <ConfigProvider button={{ autoInsertSpace: false }}>{ui}</ConfigProvider>,
    options,
  )
}
