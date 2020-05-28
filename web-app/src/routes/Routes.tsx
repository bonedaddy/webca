import React from 'react';
import { BrowserRouter as Router, Route, Switch } from 'react-router-dom';
import { SignUpContainer } from '../modules/signup';
import { NewCertificateContainer } from '../modules/certificates';
import { LoginContainer } from '../modules/login';

export function Routes() {
  return (
    <Router>
      <Switch>
        <Route exact path="/signup">
          <SignUpContainer />
        </Route>
        <Route exact path="/login">
          <LoginContainer />
        </Route>
        <Route exact path="/certificates/add">
          <NewCertificateContainer />
        </Route>
        <Route path="/">
          <div />
        </Route>
      </Switch>
    </Router>
  );
}
