import React from 'react'
import ReactDOM from 'react-dom/client'
import { ConfigProvider, theme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import App from './App'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider
      locale={zhCN}
      button={{ autoInsertSpace: false }}
      theme={{
        algorithm: theme.darkAlgorithm,
        token: {
          colorPrimary: '#1677ff',
          borderRadius: 6,
          colorBgLayout: '#0f1420',
          colorBgContainer: '#1c2333',
        },
        components: {
          Layout: { headerBg: '#141a26', siderBg: '#141a26' },
          Menu: { darkItemBg: '#141a26', darkItemSelectedBg: '#1677ff' },
        },
      }}
    >
      <App />
    </ConfigProvider>
  </React.StrictMode>,
)
