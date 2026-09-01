import { render, screen } from '@testing-library/react';
import { Button } from '../Button';

describe('Button', () => {
  it('renders its label and fires onClick', async () => {
    const onClick = vi.fn();
    render(<Button onClick={onClick}>Queue</Button>);

    const button = screen.getByRole('button', { name: 'Queue' });
    expect(button).toBeEnabled();

    button.click();
    expect(onClick).toHaveBeenCalledOnce();
  });

  it('is disabled and shows no icon while loading', () => {
    render(
      <Button loading icon="check">
        Queue
      </Button>,
    );

    expect(screen.getByRole('button', { name: 'Queue' })).toBeDisabled();
    expect(screen.queryByText('check')).toBeNull();
  });
});
