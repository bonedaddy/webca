import React, { useEffect } from 'react';
import { Provider } from "react-redux";
import { store, initState, teardown } from './state';
import { checkBackendHealth } from './api';
import { Routes } from './routes';

import 'antd/dist/antd.css';
import './App.css';

function App() {
  useEffect(() => {
    initState();
    checkBackendHealth();
    return teardown
  }, []);

  return (
    <div className="App">
      <Provider store={store}>
        <Routes />
      </Provider>
    </div>
  );
}

export default App;
