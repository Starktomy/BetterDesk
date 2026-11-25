'use strict';

/**
 * Panel session cookie options.
 *
 * With HTTPS enabled the server can still fall back to plain HTTP when the
 * certificates are missing. A fixed secure:true cookie is then never sent,
 * so nobody can log in; express-session's 'auto' marks the cookie Secure per
 * request, whenever the connection is HTTPS (directly or via a trusted
 * proxy), and leaves it out on the HTTP fallback.
 */
function sessionCookieOptions({ httpsEnabled, maxAge }) {
    return {
        secure: httpsEnabled ? 'auto' : false,
        httpOnly: true,
        sameSite: 'lax',
        maxAge
    };
}

module.exports = { sessionCookieOptions };
