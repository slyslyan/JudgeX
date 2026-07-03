import { createRouter, createWebHistory } from 'vue-router'
import Login from '../views/Login.vue'
import Register from '../views/Register.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      name: 'Home',
      component: () => import('../views/Home.vue'),
    },
    {
      path: '/playground',
      name: 'Playground',
      component: () => import('../views/Playground.vue'),
    },
    {
      path: '/login',
      name: 'Login',
      component: Login,
    },
    {
      path: '/register',
      name: 'Register',
      component: Register,
    },
    {
      path: '/problems',
      name: 'ProblemList',
      component: () => import('../views/ProblemList.vue'),
    },
    {
      path: '/problems/:id',
      name: 'ProblemDetail',
      component: () => import('../views/ProblemDetail.vue'),
    },
    {
      path: '/problems/:id/code',
      name: 'ProblemCode',
      component: () => import('../views/ProblemCode.vue'),
    },
    {
      path: '/contests',
      name: 'ContestList',
      component: () => import('../views/ContestList.vue'),
    },
    {
      path: '/contests/:id',
      name: 'ContestDetail',
      component: () => import('../views/ContestDetail.vue'),
    },
    {
      path: '/contests/:contestId/problems/:problemId',
      name: 'ContestProblem',
      component: () => import('../views/ContestProblem.vue'),
    },
    {
      path: '/profile',
      name: 'Profile',
      component: () => import('../views/Profile.vue'),
    },
    {
      path: '/users/:id',
      name: 'UserProfile',
      component: () => import('../views/Profile.vue'),
    },
    {
      path: '/submissions',
      name: 'Submissions',
      component: () => import('../views/Submissions.vue'),
    },
    {
      path: '/submissions/:id',
      name: 'SubmissionDetail',
      component: () => import('../views/SubmissionDetail.vue'),
    },
    {
      path: '/leaderboard',
      name: 'Leaderboard',
      component: () => import('../views/Leaderboard.vue'),
    },

    // Admin management (with sidebar)
    {
      path: '/admin',
      component: () => import('../views/admin/AdminLayout.vue'),
      children: [
        {
          path: 'dashboard',
          name: 'AdminDashboard',
          component: () => import('../views/admin/Dashboard.vue'),
        },
        {
          path: 'users',
          name: 'AdminUsers',
          component: () => import('../views/admin/Users.vue'),
        },
        {
          path: 'announcements',
          name: 'AdminAnnouncements',
          component: () => import('../views/admin/Announcements.vue'),
        },
        {
          path: 'problem-feedback',
          name: 'AdminProblemFeedback',
          component: () => import('../views/admin/ProblemFeedback.vue'),
        },
      ],
    },

    // Admin standalone pages (no sidebar)
    {
      path: '/admin/contests/new',
      name: 'ContestCreate',
      component: () => import('../views/admin/ContestEdit.vue'),
    },
    {
      path: '/admin/contests/:id/edit',
      name: 'ContestEdit',
      component: () => import('../views/admin/ContestEdit.vue'),
    },
    {
      path: '/admin/problems/new',
      name: 'ProblemCreate',
      component: () => import('../views/admin/ProblemEdit.vue'),
    },
    {
      path: '/admin/problems/:id/edit',
      name: 'ProblemEdit',
      component: () => import('../views/admin/ProblemEdit.vue'),
    },
    {
      path: '/admin/problems/:id/testcases',
      name: 'TestCases',
      component: () => import('../views/admin/TestCases.vue'),
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'NotFound',
      component: () => import('../views/NotFound.vue'),
    },
  ],
})

function isAuthenticated() {
  return !!localStorage.getItem('token')
}

const publicPaths = ['/login', '/register']

router.beforeEach((to) => {
  const auth = isAuthenticated()
  if (publicPaths.includes(to.path) && auth) return '/'
  if (!publicPaths.includes(to.path) && !auth) return '/login'
})

export default router
