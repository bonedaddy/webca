import React from 'react';
import { screen, wait } from '@testing-library/react';
import { act } from 'react-dom/test-utils';
import userEvent from '@testing-library/user-event';
import { SignUpContainer } from './SignUpContainer';
import { render, fireEvent } from '../../testutils';
import { mockRequests, httpclient } from '../../api/httpclient';
import { store } from '../../state';
import { removeUser } from '../../state/user/actions';

beforeEach(() => {
  store.dispatch(removeUser());
  sessionStorage.clear();
});

test('signup: renders form', async () => {
  // Assert being state
  expect(store.getState().user.user).toBeUndefined();

  const user = {
    id: 'a56b7c59-3b40-4d44-b264-21d4d9800f2c',
    email: 'test@mail.com',
    role: 'ADMIN',
    createdAt: '2020-05-16 08:30:20',
    updatedAt: '2020-05-16 08:30:20',
    account: {
      id: '8c804f01-2b26-4732-b22e-cb5fa15f29ca',
      name: 'test-account-name',
      createdAt: '2020-05-16 08:30:20',
      updatedAt: '2020-05-16 08:30:20',
    },
  };
  mockRequests({
    '/api/v1/signup': {
      body: {
        token: 'header.body.signature',
        user,
      },
      metadata: {
        method: 'GET',
        requestId: 'signup-request-id',
        status: 200,
        url: '/api/v1/signup',
      },
    },
  });

  render(<SignUpContainer />);
  const title = screen.getByText(/webca.io/);
  expect(title).toBeInTheDocument();

  const accountNameInput = screen.getByPlaceholderText(/Account name/) as HTMLInputElement;
  expect(accountNameInput).toBeInTheDocument();

  const emailInput = screen.getByPlaceholderText(/Email/) as HTMLInputElement;
  expect(emailInput).toBeInTheDocument();

  const passwordInput = screen.getByPlaceholderText(/Password/) as HTMLInputElement;
  expect(passwordInput).toBeInTheDocument();

  const signupButton = screen.getByText(/Sign Up/);
  expect(signupButton).toBeInTheDocument();

  expect(accountNameInput.value).toBe('');
  fireEvent.change(accountNameInput, { target: { value: 'test-account-name' } });
  expect(accountNameInput.value).toBe('test-account-name');

  expect(emailInput.value).toBe('');
  fireEvent.change(emailInput, { target: { value: 'test@mail.com' } });
  expect(emailInput.value).toBe('test@mail.com');

  expect(passwordInput.value).toBe('');
  fireEvent.change(passwordInput, { target: { value: '68630b4dbe30f4a3cc62e3d69552dee2' } });
  expect(passwordInput.value).toBe('68630b4dbe30f4a3cc62e3d69552dee2');

  fireEvent.click(signupButton);
  await wait(
    () => {
      const state = store.getState();
      expect(state.user.loaded).toBe(true);
      expect(state.user.user).toBe(user);
      expect(httpclient.getHeaders()['Authorization']).toBe('Bearer header.body.signature');
      expect(window.location.pathname).toBe('/');
    },
    { timeout: 1 },
  );
});

test('signup: test required fields', async () => {
  // Assert being state
  expect(store.getState().user.user).toBeUndefined();

  await act(async () => {
    render(<SignUpContainer />);
  });

  await wait(
    () => {
      const title = screen.getByText(/webca.io/);
      expect(title).toBeInTheDocument();

      // Check that required warning texts ARE NOT displayed.
      expect(screen.queryByText(/Please provide an account name/)).toBeFalsy();
      expect(screen.queryByText(/A valid email is required/)).toBeFalsy();
      expect(screen.queryByText(/At least 16 charactes are required in password/)).toBeFalsy();
    },
    { timeout: 1000 },
  );

  await wait(
    () => {
      const signupButton = screen.getByText(/Sign Up/);
      expect(signupButton).toBeInTheDocument();
      fireEvent.click(signupButton);

      // Check that required warning texts ARE displayed.
      expect(screen.queryByText(/Please provide an account name/)).toBeTruthy();
      expect(screen.queryByText(/A valid email is required/)).toBeTruthy();
      expect(screen.queryByText(/At least 16 charactes are required in password/)).toBeTruthy();
    },
    { timeout: 1000 },
  );

  const state = store.getState();
  expect(state.user.loaded).toBe(false);
  expect(state.user.user).toBeUndefined();
});

test('login: redirect to signup works', async () => {
  // Assert being state
  expect(store.getState().user.user).toBeUndefined();

  render(<SignUpContainer />);

  const signupButton = screen.getByRole('button', { name: /sign up/i });
  expect(signupButton).toBeInTheDocument();

  const loginLink = screen.getByRole('link', { name: /log in/i });
  expect(loginLink).toBeInTheDocument();

  userEvent.click(loginLink);
  expect(window.location.pathname).toBe('/login');
});

test('signup: duplicate account emails displayes error', async () => {
  // Assert being state
  expect(store.getState().user.user).toBeUndefined();
  mockRequests({
    '/api/v1/signup': {
      metadata: {
        method: 'GET',
        requestId: 'signup-request-id',
        status: 409,
        url: '/api/v1/signup',
      },
      error: new Error('Conflict'),
    },
  });

  render(<SignUpContainer />);

  const accountNameInput = screen.getByPlaceholderText(/Account name/) as HTMLInputElement;
  const emailInput = screen.getByPlaceholderText(/Email/) as HTMLInputElement;
  const passwordInput = screen.getByPlaceholderText(/Password/) as HTMLInputElement;

  fireEvent.change(accountNameInput, { target: { value: 'test-account-name' } });
  fireEvent.change(emailInput, { target: { value: 'duplicate@mail.com' } });
  fireEvent.change(passwordInput, { target: { value: '68630b4dbe30f4a3cc62e3d69552dee2' } });

  const signupButton = screen.getByText(/Sign Up/);
  fireEvent.click(signupButton);

  await wait(
    () => {
      expect(screen.getByText(/A user with that email already exits for the account/i)).toBeInTheDocument();
      const state = store.getState();
      expect(state.user.loaded).toBe(false);
      expect(state.user.user).toBeUndefined();
      expect(window.location.pathname).not.toBe('/');
    },
    { timeout: 1 },
  );
});
