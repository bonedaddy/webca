import React from 'react';
import { useDispatch } from 'react-redux';
import { AuthenticationRequest } from '../../types';
import { Login } from './components/Login';
import { login } from '../../state/user';
import { useHistory } from 'react-router-dom';

export function LoginContainer() {
  const dispatch = useDispatch();
  const history = useHistory();
  const loginCallback = (success: boolean) => {
    if (success) {
      history.push('/');
    }
  };

  const handleLogin = (req: AuthenticationRequest) => {
    dispatch(login(req, loginCallback));
  };

  return <Login submit={handleLogin} />;
}
