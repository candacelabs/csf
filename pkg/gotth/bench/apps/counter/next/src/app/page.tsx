import { redirect } from 'next/navigation';

/*
 * Every bench app is a single route (§2). `/` exists only so a human who opens
 * the container's port lands somewhere useful; it is never measured, and it is
 * a redirect rather than a second rendered page so it cannot contribute
 * elements, bytes or a route cache entry to anything the harness looks at.
 */
export default function Index() {
  redirect('/counter');
}
