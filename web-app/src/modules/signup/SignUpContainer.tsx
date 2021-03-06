import React from 'react';
import { useDispatch } from 'react-redux';
import { useHistory } from 'react-router-dom';
import { AuthenticationRequest } from '../../types';
import { SignUp } from './components/SignUp';
import { signUp } from '../../state/user';

export function SignUpContainer() {
  const dispatch = useDispatch();
  const history = useHistory();
  const onSignUp = (success: boolean) => {
    if (success) {
      history.push('/');
    }
  };

  const handleSignup = (req: AuthenticationRequest) => {
    dispatch(signUp(req, onSignUp));
  };

  return <SignUp submit={handleSignup} />;
}
